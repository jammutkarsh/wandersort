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
	"fmt"
	"os"
	stdpath "path"
	"path/filepath"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
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
	// written, no row changes. It reports the paths with the review's edits
	// applied, the same ones a real run would use.
	DryRun bool
	// OnApplied runs once the review's edits are in the plan, before any file
	// moves (never on a dry run). The review's peek copies are removed here:
	// with the plan written there is nothing left to peek at.
	OnApplied func()
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

// NotEnoughSpaceError is Run refusing a transfer the output volume can't
// hold. Nothing was changed: the check runs before the edits apply.
type NotEnoughSpaceError struct {
	Needed, Files, Reserve, Free uint64
}

func (e *NotEnoughSpaceError) Error() string {
	return fmt.Sprintf("not enough free space: the plan needs %s (%s of files, plus room for the database backup and %s kept free), only %s free at the output — nothing was changed",
		volume.HumanBytes(e.Needed), volume.HumanBytes(e.Files), volume.HumanBytes(e.Reserve), volume.HumanBytes(e.Free))
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

// Run is the whole transfer (spec D18): refuse a plan the output can't hold
// (*NotEnoughSpaceError, nothing changed), write the review's draft into the
// plan, back the database up, then perform o.Mode over every pending entry,
// placing each at outputDir/target_path, and report what happened. A dry run
// does none of the writing and reports the paths a real run would use. The
// caller holds the output lock (lock.AcquireOutput) for the same reason scan
// does: this writes to that directory.
func Run(ctx context.Context, database *db.DB, log logger.Logger, outputDir string, o Options) (Report, error) {
	xfer := productionTransfer
	if o.DryRun {
		xfer = dryRunTransfer
	}
	return run(ctx, database, log, outputDir, o, xfer)
}

// pendingRow is one file a run will place.
type pendingRow struct {
	ID         int64  `db:"id"`
	FileID     int64  `db:"file_id"`
	SourcePath string `db:"source_path"`
	TargetPath string `db:"target_path"`
	FileHash   string `db:"file_hash"`
	Size       int64  `db:"file_size"`
	ModifiedAt string `db:"file_modified_at"`
}

func (r pendingRow) scanned() scanned {
	return scanned{hash: r.FileHash, size: r.Size, modifiedAt: r.ModifiedAt}
}

func loadPending(ctx context.Context, q sqlx.QueryerContext) ([]pendingRow, error) {
	var rows []pendingRow
	if err := sqlx.SelectContext(ctx, q, &rows,
		`SELECT ve.id, ve.file_id, ve.source_path, ve.target_path, COALESCE(fm.file_hash, '') AS file_hash,
			fr.file_size, fr.file_modified_at
		FROM virtual_fs_entries ve JOIN file_registry fr ON fr.id = ve.file_id
		LEFT JOIN file_metadata fm ON fm.file_id = ve.file_id
		WHERE `+db.PendingTransfer("ve.file_id")+` ORDER BY ve.id`); err != nil {
		return nil, fmt.Errorf("load pending entries: %w", err)
	}
	return rows, nil
}

// prepare is spec D18's order before any file moves: room for the whole plan
// first, so a refusal changes nothing; then the review's edits go into the
// plan in one transaction; then the rows to place are read. A dry run applies
// the edits in a transaction it rolls back, so it reads the same rows a real
// run would, at the same paths.
func prepare(ctx context.Context, database *db.DB, outputDir string, o Options) ([]pendingRow, error) {
	if o.DryRun {
		var rows []pendingRow
		err := vfs.PreviewDraft(ctx, database, outputDir, func(ctx context.Context, q sqlx.QueryerContext) error {
			var err error
			rows, err = loadPending(ctx, q)
			return err
		})
		return rows, err
	}
	if err := checkFits(ctx, database, outputDir); err != nil {
		return nil, err
	}
	if err := vfs.ApplyDraft(ctx, database, outputDir); err != nil {
		return nil, fmt.Errorf("apply review edits: %w", err)
	}
	if o.OnApplied != nil {
		o.OnApplied()
	}
	return loadPending(ctx, database.SQL)
}

// checkFits refuses a transfer the output volume can't hold: every file not
// yet transferred (a review edit never changes a file's size, so the total is
// the same before and after the draft applies), room for the backup Run writes
// first, and a reserve so the disk is never filled to its last byte
// (volume.TransferNeeds). The database's size is its page count, which the
// backup — a VACUUM INTO — never exceeds. An unreadable free-space figure lets
// the transfer run; each file still lands whole or not at all.
//
// ponytail: a same-volume move only renames and needs no room for the files,
// but this counts them anyway. Split the check by mode (or volume) if that's
// ever the transfer someone is blocked on.
// spaceOf is volume.Space, called through a variable so a test can run out
// of room.
var spaceOf = volume.Space

func checkFits(ctx context.Context, database *db.DB, outputDir string) error {
	_, pending, err := Pending(ctx, database)
	if err != nil {
		return err
	}
	var dbBytes int64
	if err := database.SQL.GetContext(ctx, &dbBytes,
		`SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()`); err != nil {
		return fmt.Errorf("size the database: %w", err)
	}
	free, total, err := spaceOf(outputDir)
	if err != nil {
		return nil
	}
	if needed := volume.TransferNeeds(uint64(pending), uint64(dbBytes), total); needed > free {
		return &NotEnoughSpaceError{
			Needed: needed, Files: uint64(pending), Free: free,
			Reserve: needed - uint64(pending) - 2*uint64(dbBytes),
		}
	}
	return nil
}

func run(ctx context.Context, database *db.DB, log logger.Logger, outputDir string, o Options, xfer transfer) (Report, error) {
	rows, err := prepare(ctx, database, outputDir, o)
	if err != nil {
		return Report{}, err
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

		landed, size, xerr := xfer(ctx, o.Mode, src, dst, r.scanned(), commit)
		if xerr != nil && committed == "" {
			if !o.DryRun {
				if err := markFailed(database, r.FileID, xerr); err != nil {
					log.Error("could not record a failed transfer", "source", src, "error", err)
				}
			}
			rep.Failed++
			log.Warn("could not transfer file", "source", src, "target", landed, "error", xerr)
			continue
		}
		if xerr != nil {
			// Landed and recorded, then something after it went wrong (a
			// move's source that could not be removed, mostly). The library
			// holds the file, so the row is right; say so and move on.
			log.Warn("placed file, but the transfer did not finish cleanly", "source", src, "target", committed, "error", xerr)
		}
		rep.Done++
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

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("clean up placed duplicates: begin tx: %w", err)
	}
	defer tx.Rollback()
	if err := db.Forget(ctx, tx, ids); err != nil {
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

// markPlaced records the file in the library at target (db.MarkPlaced).
//
// Synchronous, unlike every other write in the pipeline: this is the one row
// whose absence the user pays for in photos. Fire-and-forget batching means
// the run can report a file placed while the batch carrying that fact was
// rolled back into a log line — and in move mode the source is gone by then.
// WriteSync returns the transaction's own error, and with synchronous=FULL a
// nil return means the row is on the disk, not merely in the page cache.
func markPlaced(database *db.DB, id, fileID int64, target string) error {
	return database.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		return db.MarkPlaced(ctx, tx, id, fileID, target)
	})
}

// markFailed records a TRANSFER error and leaves the row, and so its folder,
// where it was planned: a retry lands where the user reviewed it.
func markFailed(database *db.DB, fileID int64, xerr error) error {
	xerr = db.WithStack(xerr) // frames must be taken here, not on the writer's goroutine
	return database.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		return db.MarkFailed(ctx, tx, fileID, failedOp(xerr), xerr)
	})
}
