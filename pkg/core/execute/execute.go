// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package execute copies or moves planned files into the library. It reads
// every planned row of virtual_fs_entries whose file is not yet placed and has no TRANSFER
// error, and places that file at outputDir/target_path: success sets
// file_registry.placed, failure records an errors row. Review only ever
// writes database rows; this is the one phase that touches the user's media
// files.
//
// Deliberately sequential — see .tickets/apply-phase-unmeasured.md: nothing
// has measured this phase's throughput yet, so there is nothing to size a
// worker pool against. Resumable by construction instead of by an explicit
// state machine: it only ever selects pending rows, so a run that stops
// partway (crash, ctrl+c, a bad file) leaves every untouched row exactly
// where a second run will pick it up. A file that failed keeps its TRANSFER
// error row and is not retried automatically — the same contract a file the
// metadata phase could not read has.
package execute

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	stdpath "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
	"github.com/jammutkarsh/wandersort/pkg/core/metadata"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// Mode is Copy or Move. The zero value is Copy — the safe default per the
// design ticket: ship Copy first, gate Move behind an explicit choice.
type Mode int

const (
	ModeCopy Mode = iota
	ModeMove
)

// moveCopying is ModeMove for one source whose size or date changed since the
// scan. A same-device rename would land bytes nothing checked against the
// stored hash, so the file goes through the hash-checked copy instead and the
// source is removed after, as a move across devices is. Chosen per file by
// Run, never a Run's own Mode.
const moveCopying Mode = -1

// moves reports whether m removes the source once the file is placed.
func (m Mode) moves() bool { return m == ModeMove || m == moveCopying }

func (m Mode) String() string {
	if m.moves() {
		return "move"
	}
	return "copy"
}

// Options controls one Run.
type Options struct {
	Mode Mode
	// DryRun reports what would happen and touches nothing — no file is
	// written, no row changes.
	DryRun bool
	// OnProgress reports after each row is decided: its target path, source
	// size, and how many of the total are done. nil if the caller doesn't
	// care (the plain CLI path just reads the returned Report).
	OnProgress func(target string, bytes int64, done, total int)
}

// Report is what a Run produced.
type Report struct {
	Done, Failed int
	Bytes        int64
}

// Pending is what a transfer started right now would handle: every planned
// file not yet in the library and without a failed transfer, and their total
// size. The same rows Run reads, counted without reading them; a review edit
// never changes a file's size, so the total is the same before and after the
// draft applies.
func Pending(ctx context.Context, database *db.DB) (files int, bytes int64, err error) {
	var n struct {
		Files int   `db:"files"`
		Bytes int64 `db:"bytes"`
	}
	if err := database.SQL.GetContext(ctx, &n,
		`SELECT count(*) AS files, COALESCE(SUM(fr.file_size), 0) AS bytes
		FROM virtual_fs_entries ve JOIN file_registry fr ON fr.id = ve.file_id
		WHERE `+db.PendingTransfer("ve.file_id")); err != nil {
		return 0, 0, fmt.Errorf("count pending files: %w", err)
	}
	return n.Files, n.Bytes, nil
}

// LeftBehind lists, as source paths, every file a scan found that no transfer
// will bring into the library: it has never been read (it failed, or a run
// stopped first), so it has no hash, no plan and no place in any Report. The
// next add tries it again. Whoever is about to treat a source as done — a
// move, a card about to be formatted — needs these named, since nothing else
// in a run mentions them.
func LeftBehind(ctx context.Context, database *db.DB) ([]string, error) {
	var rows []struct {
		Dir  string `db:"file_dir"`
		Name string `db:"file_name"`
	}
	if err := database.SQL.SelectContext(ctx, &rows, `
		SELECT f.file_dir, f.file_name FROM file_registry f
		WHERE f.placed = 0
		  AND NOT EXISTS (SELECT 1 FROM file_metadata m WHERE m.file_id = f.id)
		ORDER BY f.file_dir, f.file_name`); err != nil {
		return nil, fmt.Errorf("list files left behind: %w", err)
	}
	paths := make([]string, len(rows))
	for i, r := range rows {
		paths[i] = filepath.Join(wspath.FromSourcePath(r.Dir), r.Name)
	}
	return paths, nil
}

// transfer places src at dst, creating dst's parent directories, and returns
// where the file actually landed — dst, or the next free _N name beside it
// when dst is already taken. want is the file_hash the scan stored: a copy
// whose bytes don't hash to it never lands. Atomic: the landing path either
// does not exist, or holds the complete file. The seam a fake implementation
// sits behind for tests — not an FS interface, because one function is the
// only behaviour that varies.
//
// commit is called once the file is at its landing path and verified, and
// before a move unlinks the source. That order is the whole point: the
// database records the file as being in the library before its only other
// copy is destroyed, so a crash between the two leaves a duplicate rather
// than nothing. An error from commit means the file stays where it is and the
// source is kept.
type transfer func(ctx context.Context, mode Mode, src, dst, want string, commit commitFn) (string, error)

// commitFn records one file as placed at landed, durably, before the caller
// does anything it cannot take back.
type commitFn func(landed string) error

// Run performs o.Mode over every pending entry in database, placing each at
// outputDir/target_path, and reports what happened. The caller holds the
// output lock (lock.AcquireOutput) for the same reason scan does: this writes
// to that directory.
func Run(ctx context.Context, database *db.DB, log logger.Logger, outputDir string, o Options) (Report, error) {
	xfer := productionTransfer
	if o.DryRun {
		xfer = dryRunTransfer
	}
	return run(ctx, database, log, outputDir, o, xfer)
}

func run(ctx context.Context, database *db.DB, log logger.Logger, outputDir string, o Options, xfer transfer) (Report, error) {
	var rows []struct {
		ID         int64  `db:"id"`
		FileID     int64  `db:"file_id"`
		SourcePath string `db:"source_path"`
		TargetPath string `db:"target_path"`
		FileHash   string `db:"file_hash"`
		Size       int64  `db:"file_size"`
		ModifiedAt string `db:"file_modified_at"`
	}
	// A dry run reads the same list a real run would transfer.
	if err := database.SQL.SelectContext(ctx, &rows,
		`SELECT ve.id, ve.file_id, ve.source_path, ve.target_path, COALESCE(fm.file_hash, '') AS file_hash,
			fr.file_size, fr.file_modified_at
		FROM virtual_fs_entries ve JOIN file_registry fr ON fr.id = ve.file_id
		LEFT JOIN file_metadata fm ON fm.file_id = ve.file_id
		WHERE `+db.PendingTransfer("ve.file_id")+` ORDER BY ve.id`); err != nil {
		return Report{}, fmt.Errorf("load pending entries: %w", err)
	}
	if len(rows) == 0 {
		return Report{}, nil
	}
	// Spec D24: the database is the only record of the plan and its edits.
	// Photos survive a corrupted database; their structure's meaning doesn't.
	if !o.DryRun {
		if err := database.Backup(ctx, filepath.Join(outputDir, db.BackupFileName)); err != nil {
			return Report{}, fmt.Errorf("back up database: %w", err)
		}
	}

	start := time.Now()
	var rep Report
	for i, r := range rows {
		if ctx.Err() != nil {
			break // everything left stays pending — the next run picks it up
		}

		// source_path is stored via path.ToSourcePath (separator only);
		// convert back to the OS's native form before touching the filesystem.
		src := wspath.FromSourcePath(r.SourcePath)

		// Stat before transferring, not after: a Move unlinks the source on
		// success, so "after" has nothing left to size for the report.
		info, statErr := os.Stat(src)
		dst := filepath.Join(outputDir, r.TargetPath)

		// committed is what the report counts. A file on disk that no row
		// records is the one outcome worth never reporting as success.
		var committed string
		commit := func(landed string) error {
			target := r.TargetPath
			if rel, err := filepath.Rel(outputDir, landed); err == nil {
				target = wspath.ToLibrary(rel)
			}
			if !o.DryRun {
				if err := markPlaced(database, r.ID, r.FileID, target); err != nil {
					return &stepError{opCommit, fmt.Errorf("record the placed file: %w", err)}
				}
			}
			committed = target
			return nil
		}

		var xerr error
		switch {
		case statErr != nil:
			// A missing source is not automatically a failure: a crash after
			// the file landed but before its row committed leaves exactly
			// this, and a move has already deleted the original. Ask the
			// library whether it holds the file before writing the row off —
			// the alternative is a TRANSFER error that is never retried on a
			// file that is sitting there, correct, all along.
			if landed, ok := alreadyLanded(dst, r.FileHash, r.Size); ok {
				log.Info("found the file already in the library; recording it", "target", landed)
				dst = landed
				xerr = commit(landed)
			} else {
				xerr = &stepError{opStat, fmt.Errorf("source missing: %w", statErr)}
			}
		default:
			mode := o.Mode
			if mode == ModeMove && (info.Size() != r.Size || db.FormatTime(info.ModTime()) != r.ModifiedAt) {
				mode = moveCopying
			}
			dst, xerr = xfer(ctx, mode, src, dst, r.FileHash, commit)
		}
		if errors.Is(xerr, errSourceNotRemoved) {
			// The file is in the library and already recorded there; the
			// source stays behind as a duplicate the next scan cleans up.
			log.Warn("placed file but could not remove its source", "source", src, "target", dst, "error", xerr)
			xerr = nil
		}

		if xerr != nil && committed == "" {
			if !o.DryRun {
				if err := markFailed(database, r.FileID, xerr); err != nil {
					log.Error("could not record a failed transfer", "source", src, "error", err)
				}
			}
			rep.Failed++
			log.Warn("could not transfer file", "source", src, "target", dst, "error", xerr)
			continue
		}
		if xerr != nil {
			// Landed and recorded, then something after it went wrong. The
			// library holds the file, so the row is right; say so and move on.
			log.Warn("placed file, but the transfer did not finish cleanly", "target", committed, "error", xerr)
		}
		rep.Done++
		var size int64
		if info != nil {
			size = info.Size()
		}
		rep.Bytes += size
		if o.OnProgress != nil {
			o.OnProgress(committed, size, i+1, len(rows))
		}
	}
	database.Writer.Flush()

	if err := ctx.Err(); err != nil {
		// Stopped between files: everything not reached is still pending and
		// the next run starts there. The duplicate cleanup waits for a run
		// that finishes.
		log.Info(summary(o, rep, time.Since(start).Round(time.Millisecond))+" — stopped", logger.UserKey, true)
		return rep, err
	}

	if !o.DryRun {
		// GC failure leaves duplicates a little longer; never fail the run over it
		if err := cleanupPlacedDuplicates(ctx, database, outputDir); err != nil {
			log.Warn("could not clean up placed duplicates", "error", err)
		}
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	log.Info(summary(o, rep, elapsed), logger.UserKey, true,
		logger.PhaseKey, "execute", logger.EventKey, "done", logger.ElapsedKey, elapsed.String())
	return rep, nil
}

// cleanupPlacedDuplicates hard-deletes every file_registry row (and its
// file_metadata row) whose content hash matches a placed file — the
// duplicates that lost their election, and a copy of an already-placed file a
// later scan saw again. Spec D10: only what is still in the library matters,
// and a placed file's own row is the one true record from here on. Keyed off
// file_registry.placed read fresh every run, so a run that stops early is
// picked up by the next one; nothing here touches a file unrelated to any
// placed hash.
func cleanupPlacedDuplicates(ctx context.Context, database *db.DB, outputDir string) error {
	// Every file_metadata row sharing a hash with a placed file, except the
	// placed file's own row. Read up front, before anything is deleted, so
	// the delete works from one fixed id list. The placed file's own path
	// comes along so it can be checked before its duplicates are forgotten.
	var rows []struct {
		ID          int64  `db:"id"`
		PlacedDir   string `db:"placed_dir"`
		PlacedName  string `db:"placed_name"`
		PlacedBytes int64  `db:"placed_size"`
	}
	if err := database.SQL.SelectContext(ctx, &rows, `
		SELECT fm.file_id AS id, fr2.file_dir AS placed_dir,
			fr2.file_name AS placed_name, fr2.file_size AS placed_size
		FROM file_metadata fm
		JOIN file_registry fr ON fr.id = fm.file_id
		JOIN file_metadata fm2 ON fm2.file_hash = fm.file_hash
		JOIN file_registry fr2 ON fr2.id = fm2.file_id AND fr2.placed = 1
		WHERE fr.placed = 0`); err != nil {
		return fmt.Errorf("find placed duplicates: %w", err)
	}

	// Forgetting a duplicate is only safe while the file it duplicates is
	// actually in the library. A transfer that lost its bytes — a crash, a
	// bad sector, something outside WanderSort — would otherwise take the
	// database's knowledge of every surviving copy with it, and there is no
	// other record of them. Checked by existence and size, not by hash: this
	// runs after every transfer, and re-reading the library each time is what
	// `wandersort check --full` is for.
	var ids []int64
	for _, r := range rows {
		abs := filepath.Join(outputDir, wspath.FromLibrary(stdpath.Join(r.PlacedDir, r.PlacedName)))
		if info, err := os.Stat(abs); err != nil || info.Size() != r.PlacedBytes {
			continue
		}
		ids = append(ids, r.ID)
	}
	if len(ids) == 0 {
		return nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("clean up placed duplicates: begin tx: %w", err)
	}
	defer tx.Rollback()

	// The registry rows alone, by an explicit id list: their metadata, plan
	// and error rows go with them by ON DELETE CASCADE, which is what orders it.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM file_registry WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return fmt.Errorf("clean up placed duplicates: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("clean up placed duplicates: commit: %w", err)
	}
	return nil
}

func summary(o Options, rep Report, elapsed time.Duration) string {
	verb := "Copied"
	switch {
	case o.DryRun && o.Mode == ModeMove:
		verb = "Would move"
	case o.DryRun:
		verb = "Would copy"
	case o.Mode == ModeMove:
		verb = "Moved"
	}
	msg := fmt.Sprintf("%s %d files (%s) in %s", verb, rep.Done, volume.HumanBytes(uint64(rep.Bytes)), elapsed)
	if rep.Failed > 0 {
		msg += fmt.Sprintf(" — %d failed", rep.Failed)
	}
	return msg
}

// Steps of a transfer, as recorded in errors.op.
const (
	opStat         = "stat"
	opMkdir        = "mkdir"
	opCopy         = "copy"
	opRename       = "rename"
	opHash         = "hash"
	opRemoveSource = "remove-source"
	opCommit       = "commit"
)

// stepError names the step of a transfer that failed.
type stepError struct {
	op  string
	err error
}

func (e *stepError) Error() string { return e.err.Error() }
func (e *stepError) Unwrap() error { return e.err }

// failedOp is the step xerr names, else copy — the step that moves the bytes.
func failedOp(xerr error) string {
	var step *stepError
	if errors.As(xerr, &step) {
		return step.op
	}
	return opCopy
}

// markPlaced records that the file is in the library at target. It repoints
// file_registry and the row's own source_path there — library-relative, the
// same value target_path already holds (spec D9/D10): the file now lives
// under outputDir, not wherever it was scanned from, and a database that
// travels with the library must not depend on where the library is mounted. A
// stale source_path would also break a Move outright (the source is gone) and
// would leave a Copy's row pointing a reorg attempt at a location that no
// longer reflects the plan that was executed. It sets placed and clears the
// file's error rows in the same transaction.
//
// Synchronous, unlike every other write in the pipeline: this is the one row
// whose absence the user pays for in photos. Fire-and-forget batching means
// the run can report a file placed while the batch carrying that fact was
// rolled back into a log line — and in move mode the source is gone by then.
// WriteSync returns the transaction's own error, and with synchronous=FULL a
// nil return means the row is on the disk, not merely in the page cache.
func markPlaced(database *db.DB, id, fileID int64, target string) error {
	dir, name := stdpath.Dir(target), stdpath.Base(target)
	return database.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_fs_entries SET source_path = ?, target_path = ? WHERE id = ?`,
			target, target, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE file_registry SET file_dir = ?, file_name = ?, placed = 1 WHERE id = ?`, dir, name, fileID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM errors WHERE file_id = ?`, fileID)
		return err
	})
}

// markFailed records a TRANSFER error and leaves the row, and so its folder,
// where it was planned: a retry lands where the user reviewed it.
func markFailed(database *db.DB, fileID int64, xerr error) error {
	xerr = db.WithStack(xerr) // frames must be taken here, not on the writer's goroutine
	return database.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		return db.RecordError(ctx, tx, fileID, db.StageTransfer, failedOp(xerr), xerr)
	})
}

// maxLandedProbe bounds the search for an already-landed file: dst and the
// _N names beside it up to this. A library with more collisions than this on
// one name has a bigger problem than a missed match.
const maxLandedProbe = 64

// alreadyLanded answers the one question a missing source leaves open: did
// this file already land, and only the row saying so go missing? It looks at
// the planned name and the _N names beside it for a file of the scanned size
// whose bytes hash to what the scan recorded. Nothing else can tell a
// crashed-mid-run file apart from a source the user deleted, and the two
// deserve opposite answers.
//
// Every name up to maxLandedProbe is looked at, not just the unbroken run
// from dst: a _1 someone deleted leaves a gap, and the file can still be
// sitting at _2. A missing name costs one stat and a wrong-size one no read,
// so only a real candidate is hashed.
func alreadyLanded(dst, want string, size int64) (string, bool) {
	if want == "" {
		return "", false
	}
	for n := 0; n < maxLandedProbe; n++ {
		p := withSuffix(dst, n)
		if info, err := os.Stat(p); err != nil || info.Size() != size {
			continue
		}
		if got, err := metadata.HashFile(p); err == nil && got == want {
			return p, true
		}
	}
	return "", false
}

// errSourceNotRemoved means the file landed, verified, but a move could not remove
// its source. Run counts the file placed anyway: the library holds it, and
// commit has already recorded it there.
var errSourceNotRemoved = errors.New("placed, but the source could not be removed")

// productionTransfer places src at dst, or at the first free dst_N beside
// it: nothing on disk is ever replaced (spec D21). vfs.Confirm already made
// every planned path unique among the rows it knows; this is the net for
// files the database doesn't know about. The link-based atomicfile.Rename/
// Copy fail with fs.ErrExist instead of overwriting, and that is the only
// error that moves on to the next name.
//
// A taken name already holding this very file is where it lands: a crash
// between an earlier copy landing and its row committing, or `wandersort
// admin db --restore` putting back rows as pending whose files are already
// placed. Taking the next _N there would place the file twice.
func productionTransfer(ctx context.Context, mode Mode, src, dst, want string, commit commitFn) (string, error) {
	if err := ctx.Err(); err != nil {
		return dst, err
	}
	if err := atomicfile.MkdirAll(filepath.Dir(dst)); err != nil {
		return dst, &stepError{opMkdir, fmt.Errorf("create dest dir: %w", err)}
	}
	for n := 0; ; n++ {
		target := withSuffix(dst, n)
		// A taken name is seen before a byte is copied: a copy only learns
		// it at the final link, so every collision used to cost the whole
		// file again. The link still refuses a name taken in between.
		var err error
		if _, statErr := os.Lstat(target); statErr == nil {
			err = fs.ErrExist
		} else {
			err = placeFile(mode, src, target, want, func() error { return commit(target) })
		}
		if !errors.Is(err, fs.ErrExist) {
			return target, err
		}
		if holds(target, src, want) {
			if !mode.moves() {
				return target, commit(target)
			}
			// The library copy matching the scan says nothing about the
			// source: an edit since then, same size, would be deleted here
			// for good (spec D22). Rare path, so the extra read is cheap.
			got, err := metadata.HashFile(src)
			if err != nil {
				return target, &stepError{opHash, fmt.Errorf("verify source before removing it: %w", err)}
			}
			if got != want {
				return target, &stepError{opHash, fmt.Errorf("source changed since it was scanned: source %s, scanned %q: %w", got, want, db.ErrChecksumMismatch)}
			}
			if err := commit(target); err != nil {
				return target, err
			}
			return target, removeSource(src)
		}
	}
}

// holds reports whether p is already a copy of src: the same size, and the
// hash the scan stored for src. Size first, so an unrelated file taking the
// name is never read.
func holds(p, src, want string) bool {
	pi, err := os.Stat(p)
	if err != nil || want == "" {
		return false
	}
	si, err := os.Stat(src)
	if err != nil || si.Size() != pi.Size() {
		return false
	}
	got, err := metadata.HashFile(p)
	return err == nil && got == want
}

// removeSource finishes a move whose file is already verified in place.
func removeSource(src string) error {
	if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &stepError{opRemoveSource, fmt.Errorf("%w: %w", errSourceNotRemoved, err)}
	}
	return nil
}

// withSuffix names the n-th alternative for p: p itself, then name_1.ext,
// name_2.ext, …
func withSuffix(p string, n int) string {
	if n == 0 {
		return p
	}
	ext := filepath.Ext(p)
	return fmt.Sprintf("%s_%d%s", strings.TrimSuffix(p, ext), n, ext)
}

// place puts src at exactly dst, failing with fs.ErrExist if dst is taken.
// Move tries a same-device no-replace rename first — atomic, nothing copied;
// Run only allows it for a source whose size and date still match the scan
// (moveCopying otherwise), which is the check a rename gets. Any other failure (cross-device, mostly) falls back
// to copy, except a source that can't be removed, which a copy would fail on
// too. A copy hashes the bytes as it writes them and lands only if they hash
// to want (spec D22) — a source changed since the scan is not the file that
// was planned. Copy never unlinks src; Move only does once the copy is
// verified and commit has recorded it, so the source is never lost to a
// partial write, a wrong write, or a row that never made it to disk, and an
// occupied dst never loses it at all.
// placeFile is place, called through a variable so a test can see which
// names productionTransfer tries to write to.
var placeFile = place

func place(mode Mode, src, dst, want string, commit func() error) error {
	if mode == ModeMove {
		// The commit sits inside the rename, between the new name landing
		// and the old one going: the database records the file in the
		// library while it still has its source name too. Committing after
		// the rename instead left a crash window with the file moved and
		// nothing recording it — and a scan before the next execute then
		// swept the source's row, leaving a library file nothing tracks.
		var commitErr error
		err := atomicfile.RenameCommit(src, dst, func() error {
			commitErr = commit()
			return commitErr
		})
		switch {
		case err == nil:
			return nil
		case commitErr != nil:
			return err // not recorded, so the move was undone
		case errors.Is(err, atomicfile.ErrSourceLeft):
			return &stepError{opRemoveSource, fmt.Errorf("%w: %w", errSourceNotRemoved, err)}
		case errors.Is(err, fs.ErrExist):
			return err
		case errors.Is(err, atomicfile.ErrSourceKept):
			return &stepError{opRename, err}
		}
		// anything else — another device, mostly — falls back to a copy
	}

	h := metadata.NewHasher()
	if _, err := atomicfile.Copy(src, dst, h, func() error {
		if got := metadata.HashString(h); got != want {
			return fmt.Errorf("source changed since it was scanned: copied %s, scanned %q: %w", got, want, db.ErrChecksumMismatch)
		}
		return nil
	}); err != nil {
		return &stepError{opCopy, err}
	}
	// The file is in the library and verified. Record it before the source is
	// unlinked: the other order leaves a crash holding a library file nothing
	// knows about and no source to re-read it from.
	if err := commit(); err != nil {
		// Not recorded, so not kept: the source is untouched, and a library
		// copy no row accounts for would sit beside the retry's own. This
		// name was free until the copy above took it, so it is ours to remove.
		if rerr := os.Remove(dst); rerr != nil {
			return errors.Join(err, fmt.Errorf("the copy stays at %s, unrecorded: %w", dst, rerr))
		}
		return err
	}
	if !mode.moves() {
		return nil
	}
	return removeSource(src)
}

// dryRunTransfer touches no file — Run already sized and error-checked the
// source via os.Stat before calling the transfer, so a dry run's Report is
// real numbers for zero I/O. It reports the planned name: a real run may land
// on name_N instead if that name is already taken on disk. commit is still
// called, and still writes nothing: it is what counts the row as done.
func dryRunTransfer(_ context.Context, _ Mode, _, dst, _ string, commit commitFn) (string, error) {
	return dst, commit(dst)
}
