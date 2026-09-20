// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package execute is the phase vfs.go's package doc has always promised:
// "a future Execute phase performs the copy/move." It reads every APPROVED
// row of virtual_fs_entries and places that file at outputDir/target_path,
// then marks the row DONE or ERROR (+ why). Review only ever writes database
// rows; this is the one phase that touches the user's media files.
//
// Deliberately sequential — see .tickets/apply-phase-unmeasured.md: nothing
// has measured this phase's throughput yet, so there is nothing to size a
// worker pool against. Resumable by construction instead of by an explicit
// state machine: it only ever selects APPROVED rows, so a run that stops
// partway (crash, ctrl+c, a bad file) leaves every untouched row exactly
// where a second run will pick it up. A row that failed is left at ERROR,
// not retried automatically — the same contract file_registry's scan_status
// already has.
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

func (m Mode) String() string {
	if m == ModeMove {
		return "move"
	}
	return "copy"
}

// Options controls one Run.
type Options struct {
	Mode Mode
	// DryRun reports what would happen and touches nothing — no file is
	// written, no row's status changes.
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

// transfer places src at dst, creating dst's parent directories, and returns
// where the file actually landed — dst, or the next free _N name beside it
// when dst is already taken. want is the file_hash the scan stored: a copy
// whose bytes don't hash to it never lands. Atomic: the landing path either
// does not exist, or holds the complete file. The seam a fake implementation
// sits behind for tests — not an FS interface, because one function is the
// only behaviour that varies.
type transfer func(ctx context.Context, mode Mode, src, dst, want string) (string, error)

// Run performs o.Mode over every APPROVED entry in database, placing each at
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
	}
	// A dry run also counts the plan not yet approved — execute approves it
	// only once it really transfers, so otherwise a first dry run reports
	// nothing. ponytail: those rows show their paths as proposed, without the
	// review's draft edits; replay the draft here if a dry run must show them.
	pending := db.StatusApproved
	if o.DryRun {
		pending = db.StatusProposed
	}
	if err := database.SQL.SelectContext(ctx, &rows,
		`SELECT ve.id, ve.file_id, ve.source_path, ve.target_path, COALESCE(fm.file_hash, '') AS file_hash
		FROM virtual_fs_entries ve LEFT JOIN file_metadata fm ON fm.file_id = ve.file_id
		WHERE ve.status IN (?, ?) ORDER BY ve.id`,
		db.StatusApproved, pending); err != nil {
		return Report{}, fmt.Errorf("load approved entries: %w", err)
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
			break // everything left stays APPROVED — the next run picks it up
		}

		// source_path is stored via path.ToSourcePath (separator only);
		// convert back to the OS's native form before touching the filesystem.
		src := wspath.FromSourcePath(r.SourcePath)

		// Stat before transferring, not after: a Move unlinks the source on
		// success, so "after" has nothing left to size for the report.
		info, statErr := os.Stat(src)
		dst := filepath.Join(outputDir, r.TargetPath)

		var xerr error
		switch {
		case statErr != nil:
			xerr = fmt.Errorf("source missing: %w", statErr)
		default:
			dst, xerr = xfer(ctx, o.Mode, src, dst, r.FileHash)
		}
		if errors.Is(xerr, errSourceNotRemoved) {
			// The file is in the library, verified: record it there, or the
			// library holds a file the database doesn't know about. The
			// source stays behind as a duplicate the next scan cleans up.
			log.Warn("placed file but could not remove its source", "source", src, "target", dst, "error", xerr)
			xerr = nil
		}
		target := r.TargetPath
		if rel, err := filepath.Rel(outputDir, dst); err == nil {
			target = wspath.ToLibrary(rel)
		}

		if !o.DryRun {
			markResult(database, r.ID, r.FileID, target, xerr)
		}
		if xerr != nil {
			rep.Failed++
			log.Warn("could not transfer file", "source", src, "target", dst, "error", xerr)
			continue
		}
		rep.Done++
		var size int64
		if info != nil {
			size = info.Size()
		}
		rep.Bytes += size
		if o.OnProgress != nil {
			o.OnProgress(target, size, i+1, len(rows))
		}
	}
	database.Writer.Flush()

	if !o.DryRun {
		// GC failure leaves duplicates a little longer; never fail the run over it
		if err := cleanupPlacedDuplicates(ctx, database); err != nil {
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
// duplicates the scorer didn't elect, and a copy of an already-placed file a
// later scan saw again. Spec D10: only what is still in the library matters,
// and a placed file's own row is the one true record from here on. Keyed off
// file_registry.placed read fresh every run, so a run that stops early is
// picked up by the next one; nothing here touches an ERROR row or a file
// unrelated to any placed hash.
func cleanupPlacedDuplicates(ctx context.Context, database *db.DB) error {
	// Every file_metadata row sharing a hash with a placed file, except the
	// placed file's own row. Read up front, before anything is deleted, so
	// the three statements below share one fixed id list instead of each
	// re-deriving it against tables the earlier ones already changed.
	var ids []int64
	if err := database.SQL.SelectContext(ctx, &ids, `
		SELECT fm.file_id FROM file_metadata fm
		JOIN file_registry fr ON fr.id = fm.file_id
		WHERE fr.placed = 0
		AND fm.file_hash IN (
			SELECT fm2.file_hash FROM file_metadata fm2
			JOIN file_registry fr2 ON fr2.id = fm2.file_id
			WHERE fr2.placed = 1)`); err != nil {
		return fmt.Errorf("find placed duplicates: %w", err)
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

	// Same FK order as the scanner's sweep (vfs entries, then metadata, then
	// the registry row) — an explicit id list, not a reliance on file_id
	// happening to go NULL after the registry row is gone.
	statements := []string{
		`DELETE FROM virtual_fs_entries WHERE file_id IN (` + placeholders + `)`,
		`DELETE FROM file_metadata WHERE file_id IN (` + placeholders + `)`,
		`DELETE FROM file_registry WHERE id IN (` + placeholders + `)`,
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
			return fmt.Errorf("clean up placed duplicates: %w", err)
		}
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

// markResult flips one row to DONE or ERROR. On success it also repoints
// file_registry and the row's own source_path at target — library-relative,
// the same value target_path already holds (spec D9/D10): the file now lives
// under outputDir, not wherever it was scanned from, and a database that
// travels with the library must not depend on where the library is mounted.
// A stale source_path would also break a Move outright (the source is gone)
// and would leave a Copy's row pointing a reorg attempt at a location that no
// longer reflects the plan that was executed. Fire-and-forget through the
// same FIFO writer every phase uses; Run's Flush before returning is what
// makes the caller's very next read (the CLI's summary, the review tree's
// status line) see it.
func markResult(database *db.DB, id, fileID int64, target string, xerr error) {
	if xerr != nil {
		msg := xerr.Error()
		database.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx,
				`UPDATE virtual_fs_entries SET status = ?, error = ? WHERE id = ?`, db.StatusError, msg, id)
			return err
		})
		return
	}
	dir, name := stdpath.Dir(target), stdpath.Base(target)
	database.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_fs_entries SET status = ?, error = NULL, source_path = ?, target_path = ? WHERE id = ?`,
			db.StatusDone, target, target, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE file_registry SET file_dir = ?, file_name = ?, placed = 1 WHERE id = ?`, dir, name, fileID)
		return err
	})
}

// errSourceNotRemoved means the file landed, verified, but a move could not remove
// its source. Run records the row DONE anyway: the library holds the file.
var errSourceNotRemoved = errors.New("placed, but the source could not be removed")

// productionTransfer places src at dst, or at the first free dst_N beside
// it: nothing on disk is ever replaced (spec D21). vfs.Confirm already made
// every planned path unique among the rows it knows; this is the net for
// files the database doesn't know about. The link-based atomicfile.Rename/
// Copy fail with fs.ErrExist instead of overwriting, and that is the only
// error that moves on to the next name.
//
// A taken name already holding this very file is where it lands: a crash
// before the async writer recorded an earlier copy, or `wandersort recover`
// putting rows back to APPROVED whose files are already placed. Taking the
// next _N there would place the file twice.
func productionTransfer(ctx context.Context, mode Mode, src, dst, want string) (string, error) {
	if err := ctx.Err(); err != nil {
		return dst, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return dst, fmt.Errorf("create dest dir: %w", err)
	}
	for n := 0; ; n++ {
		target := withSuffix(dst, n)
		err := place(mode, src, target, want)
		if !errors.Is(err, fs.ErrExist) {
			return target, err
		}
		if holds(target, src, want) {
			if mode != ModeMove {
				return target, nil
			}
			// The library copy matching the scan says nothing about the
			// source: an edit since then, same size, would be deleted here
			// for good (spec D22). Rare path, so the extra read is cheap.
			got, err := metadata.HashFile(src)
			if err != nil {
				return target, fmt.Errorf("verify source before removing it: %w", err)
			}
			if got != want {
				return target, fmt.Errorf("source changed since it was scanned: source %s, scanned %q", got, want)
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
		return fmt.Errorf("%w: %w", errSourceNotRemoved, err)
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
// Move tries a same-device no-replace rename first — atomic, nothing copied,
// so nothing to verify; any other failure (cross-device, mostly) falls back
// to copy, except a source that can't be removed, which a copy would fail on
// too. A copy hashes the bytes as it writes them and lands only if they hash
// to want (spec D22) — a source changed since the scan is not the file that
// was planned. Copy never unlinks src; Move only does once the copy is
// verified, so the source is never lost to a partial or wrong write, and an
// occupied dst never loses it at all.
func place(mode Mode, src, dst, want string) error {
	if mode == ModeMove {
		err := atomicfile.Rename(src, dst)
		if err == nil || errors.Is(err, fs.ErrExist) || errors.Is(err, atomicfile.ErrSourceKept) {
			return err
		}
	}

	h := metadata.NewHasher()
	if _, err := atomicfile.Copy(src, dst, h, func() error {
		if got := metadata.HashString(h); got != want {
			return fmt.Errorf("source changed since it was scanned: copied %s, scanned %q", got, want)
		}
		return nil
	}); err != nil {
		return err
	}
	if mode != ModeMove {
		return nil
	}
	return removeSource(src)
}

// dryRunTransfer does nothing — Run already sized and error-checked the
// source via os.Stat before calling the transfer, so a dry run's Report is
// real numbers for zero I/O. It reports the planned name: a real run may land
// on name_N instead if that name is already taken on disk.
func dryRunTransfer(_ context.Context, _ Mode, _, dst, _ string) (string, error) { return dst, nil }
