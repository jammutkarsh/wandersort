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
	"fmt"
	"os"
	"path/filepath"
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

// transfer places src at dst, creating dst's parent directories. Atomic: dst
// either does not exist, or holds the complete file. The seam a fake
// implementation sits behind for tests — not an FS interface, because one
// function is the only behaviour that varies.
type transfer func(ctx context.Context, mode Mode, src, dst string) error

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
		SourcePath string `db:"source_path"`
		TargetPath string `db:"target_path"`
	}
	if err := database.SQL.SelectContext(ctx, &rows,
		`SELECT id, source_path, target_path FROM virtual_fs_entries WHERE status = ? ORDER BY id`,
		db.StatusApproved); err != nil {
		return Report{}, fmt.Errorf("load approved entries: %w", err)
	}
	if len(rows) == 0 {
		return Report{}, nil
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
			xerr = xfer(ctx, o.Mode, r.SourcePath, dst)
		}

		if !o.DryRun {
			markResult(database, r.ID, xerr)
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
			o.OnProgress(r.TargetPath, size, i+1, len(rows))
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

// markResult flips one row to DONE or ERROR. Fire-and-forget through the
// same FIFO writer every phase uses; Run's Flush before returning is what
// makes the caller's very next read (the CLI's summary, the picker's status
// line) see it.
func markResult(database *db.DB, id int64, xerr error) {
	if xerr != nil {
		msg := xerr.Error()
		database.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx,
				`UPDATE virtual_fs_entries SET status = ?, error = ? WHERE id = ?`, db.StatusError, msg, id)
			return err
		})
		return
	}
	database.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE virtual_fs_entries SET status = ?, error = NULL WHERE id = ?`, db.StatusDone, id)
		return err
	})
}

// productionTransfer places src at dst. Move tries a same-device os.Rename
// first — atomic, nothing copied; anything else (cross-device, or any other
// rename failure) falls back to copy. Copy never unlinks src; Move only does
// once the destination is verified complete by size, so a crash mid-copy
// never loses the source over a partial write. A destination collision is
// never overwritten here — vfs.Confirm already resolved every path to be
// unique before a row could reach APPROVED.
func productionTransfer(ctx context.Context, mode Mode, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if mode == ModeMove {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("create dest dir: %w", err)
		}
		if err := os.Rename(src, dst); err == nil {
			return nil
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
// real numbers for zero I/O.
func dryRunTransfer(context.Context, Mode, string, string) error { return nil }
