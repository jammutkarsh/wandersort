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
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
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
// when dst is already taken. Atomic: the landing path either does not exist,
// or holds the complete file. The seam a fake implementation sits behind for
// tests — not an FS interface, because one function is the only behaviour
// that varies.
type transfer func(ctx context.Context, mode Mode, src, dst string) (string, error)

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
	}
	if err := database.SQL.SelectContext(ctx, &rows,
		`SELECT id, file_id, source_path, target_path FROM virtual_fs_entries WHERE status = ? ORDER BY id`,
		db.StatusApproved); err != nil {
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

		// Stat before transferring, not after: a Move unlinks the source on
		// success, so "after" has nothing left to size for the report.
		info, statErr := os.Stat(r.SourcePath)
		dst := filepath.Join(outputDir, r.TargetPath)

		var xerr error
		switch {
		case statErr != nil:
			xerr = fmt.Errorf("source missing: %w", statErr)
		default:
			dst, xerr = xfer(ctx, o.Mode, r.SourcePath, dst)
		}
		target := r.TargetPath
		if rel, err := filepath.Rel(outputDir, dst); err == nil {
			target = filepath.ToSlash(rel)
		}

		if !o.DryRun {
			markResult(database, r.ID, r.FileID, dst, target, xerr)
		}
		if xerr != nil {
			rep.Failed++
			log.Warn("could not transfer file", "source", r.SourcePath, "target", dst, "error", xerr)
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

	elapsed := time.Since(start).Round(time.Millisecond)
	log.Info(summary(o, rep, elapsed), logger.UserKey, true,
		logger.PhaseKey, "execute", logger.EventKey, "done", logger.ElapsedKey, elapsed.String())
	return rep, nil
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
// file_registry and the row's own source_path at newPath, and target_path at
// target (newPath relative to the output folder, which differs from the plan
// when the planned name was taken on disk) — the file really
// lives there now, so a stale old path would break the next thing that reads
// it: a Move's source is gone outright, and a Copy's row would otherwise keep
// pointing reorg attempts at a location that no longer reflects the plan
// that was executed. Fire-and-forget through the same FIFO writer every phase
// uses; Run's Flush before returning is what makes the caller's very next
// read (the CLI's summary, the review tree's status line) see it.
func markResult(database *db.DB, id, fileID int64, newPath, target string, xerr error) {
	if xerr != nil {
		msg := xerr.Error()
		database.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx,
				`UPDATE virtual_fs_entries SET status = ?, error = ? WHERE id = ?`, db.StatusError, msg, id)
			return err
		})
		return
	}
	dir, name := filepath.Dir(newPath), filepath.Base(newPath)
	database.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_fs_entries SET status = ?, error = NULL, source_path = ?, target_path = ? WHERE id = ?`,
			db.StatusDone, newPath, target, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE file_registry SET file_dir = ?, file_name = ? WHERE id = ?`, dir, name, fileID)
		return err
	})
}

// productionTransfer places src at dst, or at the first free dst_N beside
// it: nothing on disk is ever replaced (spec D21). vfs.Confirm already made
// every planned path unique among the rows it knows; this is the net for
// files the database doesn't know about. The link-based atomicfile.Rename/
// Copy fail with fs.ErrExist instead of overwriting, and that is the only
// error that moves on to the next name.
//
// ponytail: results reach the database through the async writer (~100ms
// batches), so a hard crash (SIGKILL, power loss) can leave a copied file on
// disk with its row still APPROVED; the next run finds the name taken and
// places a second copy at name_1. A half-done same-device move resumes
// cleanly (atomicfile.Rename finishes it). Ticket 08 closes the copy case by
// recognising an existing file whose hash matches the source's.
func productionTransfer(ctx context.Context, mode Mode, src, dst string) (string, error) {
	if err := ctx.Err(); err != nil {
		return dst, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return dst, fmt.Errorf("create dest dir: %w", err)
	}
	for n := 0; ; n++ {
		target := withSuffix(dst, n)
		if err := place(mode, src, target); !errors.Is(err, fs.ErrExist) {
			return target, err
		}
	}
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
// any other failure (cross-device, mostly) falls back to copy, except a
// source that can't be removed, which a copy would fail on too. Copy never
// unlinks src; Move only does once the destination is verified complete by
// size, so a crash mid-copy never loses the source over a partial write, and
// an occupied dst never loses it at all.
func place(mode Mode, src, dst string) error {
	if mode == ModeMove {
		err := atomicfile.Rename(src, dst)
		if err == nil || errors.Is(err, fs.ErrExist) || errors.Is(err, atomicfile.ErrSourceKept) {
			return err
		}
	}

	n, err := atomicfile.Copy(src, dst)
	if err != nil {
		return err
	}
	if mode != ModeMove {
		return nil
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("verify source before removing it: %w", err)
	}
	if info.Size() != n {
		return fmt.Errorf("copied %d bytes but source is %d — refusing to remove source", n, info.Size())
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("copied successfully but could not remove source: %w", err)
	}
	return nil
}

// dryRunTransfer does nothing — Run already sized and error-checked the
// source via os.Stat before calling the transfer, so a dry run's Report is
// real numbers for zero I/O. It reports the planned name: a real run may land
// on name_N instead if that name is already taken on disk.
func dryRunTransfer(_ context.Context, _ Mode, _, dst string) (string, error) { return dst, nil }
