// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package verify answers the one question nothing else in WanderSort ever
// asks again: is what the database recorded still true on disk?
//
// Every placed file's content hash is stored at scan time and checked once,
// while the copy is being written. After that the bytes are never read again,
// so bitrot, a bad sector, a sync client rewriting a file, or a backup tool
// truncating one are all invisible — and execute's duplicate cleanup discards
// the database's knowledge of every other copy of that file on the strength
// of a `placed` flag alone. This package is what makes the stored hash worth
// storing: a library-wide re-check of the files, and an integrity check of
// the database holding the plan that names them.
//
// Deliberately sequential, like execute and for the same reason: nothing has
// measured this yet, so there is nothing to size a worker pool against. A
// full verify is bound by reading every byte in the library.
package verify

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

	"github.com/jammutkarsh/wandersort/pkg/core/metadata"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// Steps of a verify, as recorded in errors.op.
const (
	opStat = "stat"
	opHash = "hash"
)

// Options controls one Run.
type Options struct {
	// Full re-reads every placed file and compares its bytes against the hash
	// the scan stored. Without it a file is checked for being there and being
	// the right size, which catches a deletion or a truncation for the cost of
	// a stat — the two failures a user is most likely to have caused
	// themselves — but not a changed byte.
	Full bool
	// OnProgress reports after each file is checked.
	OnProgress func(path string, done, total int)
}

// Problem is one file that is not what the database says it is.
type Problem struct {
	// Path is library-relative, the way the row stores it.
	Path string
	Kind string
	// Detail is the human-readable reason, hashes included where they differ.
	Detail string
}

// Report is what a Run found.
type Report struct {
	// Checked is the number of placed files looked at; Bytes their total size.
	Checked int
	Bytes   int64
	// Problems is every file that is still there but not what was recorded,
	// in the order they were checked.
	Problems []Problem
	// Forgotten is every placed file that is gone from the library, by its
	// library-relative path. Its records are deleted: the library no longer
	// holds it, so nothing should claim it does, and a copy still at a source
	// is planned again by the next add instead of being skipped as placed.
	Forgotten []string
	// Database is SQLite's own verdict on the file holding the plan: "ok", or
	// the first thing it found wrong.
	Database string
	// Strays are leftover .copy-* temp files: a crash during a transfer, at
	// full file size, in the user's own folders. Reported, never deleted —
	// removing files is execute's job and a verify that deletes is a verify
	// nobody runs twice.
	Strays []string
}

// Sound reports whether the library is entirely as recorded — forgotten files
// included, since their records now say they are gone.
func (r Report) Sound() bool {
	return len(r.Problems) == 0 && len(r.Strays) == 0 && r.Database == "ok"
}

// Run checks every placed file against what the database recorded for it, and
// the database against itself. The caller holds the output lock, the same
// contract scan and execute have.
//
// A file that verifies has its VERIFY error row cleared, and one that fails
// gets a fresh one: the errors table holds only live problems (spec D29), so
// `wandersort admin report` ships exactly the failures that are still true.
//
// A file that is gone is not a problem to keep reporting but a fact to record:
// its rows are deleted once the walk is done (Report.Forgotten), after a
// backup of the database, so the next check does not list it again and a
// copy at a source can be planned in again. A file that is there but wrong —
// a different size, different bytes, unreadable — keeps its rows: that is
// damage to look at, not a deletion to accept.
//
// Nothing is logged per file: the caller reports the files, grouped, and a
// per-file warning line beside that list only said everything twice.
func Run(ctx context.Context, database *db.DB, log logger.Logger, outputDir string, o Options) (Report, error) {
	var rows []struct {
		FileID int64  `db:"file_id"`
		Dir    string `db:"file_dir"`
		Name   string `db:"file_name"`
		Size   int64  `db:"file_size"`
		Hash   string `db:"file_hash"`
	}
	// Placed rows only: an unplaced file still lives at its source, where the
	// user may legitimately have changed it since, and the plan is a proposal
	// about it rather than a record of it.
	if err := database.SQL.SelectContext(ctx, &rows, `
		SELECT fr.id AS file_id, fr.file_dir, fr.file_name, fr.file_size,
			COALESCE(fm.file_hash, '') AS file_hash
		FROM file_registry fr LEFT JOIN file_metadata fm ON fm.file_id = fr.id
		WHERE fr.placed = 1 ORDER BY fr.id`); err != nil {
		return Report{}, fmt.Errorf("load placed files: %w", err)
	}

	start := time.Now()
	rep := Report{Database: "not checked"}
	var gone []int64
	for i, r := range rows {
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		// A placed row's dir and name are library-relative (spec D9/D10), so
		// the library can be mounted anywhere and still be checkable.
		rel := wspath.FromLibrary(filepath.Join(r.Dir, r.Name))
		abs := filepath.Join(outputDir, rel)

		rep.Checked++
		problem, size := checkFile(abs, rel, r.Size, r.Hash, o.Full)
		rep.Bytes += size
		if problem != nil && problem.Kind == db.KindNotFound {
			gone = append(gone, r.FileID)
			rep.Forgotten = append(rep.Forgotten, rel)
			log.Info("placed file is gone from the library; forgetting it", "path", rel)
			if o.OnProgress != nil {
				o.OnProgress(rel, i+1, len(rows))
			}
			continue
		}
		if problem != nil {
			rep.Problems = append(rep.Problems, *problem)
			log.Info("placed file does not match the library's record",
				"path", rel, "kind", problem.Kind, "detail", problem.Detail)
		}
		if err := record(database, r.FileID, problem); err != nil {
			log.Warn("could not record the verify result", "path", rel, "error", err)
		}
		if o.OnProgress != nil {
			o.OnProgress(rel, i+1, len(rows))
		}
	}

	database.Writer.Flush() // every result recorded before the report says so

	if err := forget(ctx, database, outputDir, gone); err != nil {
		return rep, err
	}

	var err error
	if rep.Database, err = checkDatabase(ctx, database); err != nil {
		return rep, err
	}
	if rep.Strays, err = findStrays(outputDir); err != nil {
		log.Warn("could not look for leftover temp files", "error", err)
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	log.Info(summary(rep, o, elapsed), logger.UserKey, true,
		logger.PhaseKey, "verify", logger.EventKey, "done", logger.ElapsedKey, elapsed.String())
	return rep, nil
}

// checkFile decides whether one placed file is still what was recorded, and
// returns its size for the report. Size is checked before the hash so a
// truncated file is named for what it is, and so a quick pass costs one stat.
func checkFile(abs, rel string, want int64, hash string, full bool) (*Problem, int64) {
	info, err := os.Stat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &Problem{rel, db.KindNotFound, "the file is not in the library any more"}, 0
	case err != nil:
		return &Problem{rel, db.KindIO, err.Error()}, 0
	case info.IsDir():
		return &Problem{rel, db.KindOther, "a directory is where the file should be"}, 0
	}
	if info.Size() != want {
		return &Problem{
			rel, db.KindOther,
			fmt.Sprintf("size is %s, the library recorded %s",
				volume.HumanBytes(uint64(info.Size())), volume.HumanBytes(uint64(want))),
		}, info.Size()
	}
	if !full || hash == "" {
		return nil, info.Size()
	}
	got, err := metadata.HashFile(abs)
	if err != nil {
		return &Problem{rel, db.KindIO, fmt.Sprintf("could not read it: %v", err)}, info.Size()
	}
	if got != hash {
		// Same size, different bytes: nothing the user did by accident, and
		// the one failure only a full pass can see.
		return &Problem{
			rel, db.KindChecksumMismatch,
			fmt.Sprintf("contents changed: now %s, scanned %s", got, hash),
		}, info.Size()
	}
	return nil, info.Size()
}

// record keeps the errors table holding only what is still true: a failure
// replaces the file's VERIFY row, a pass removes it. Batched through the
// writer, not WriteSync: a synced transaction per file flushed the drive's
// cache once per placed file — most of a 100k-file check's run time on a
// hard disk — and a lost result only costs one re-check. Run flushes before
// it reports.
func record(database *db.DB, fileID int64, p *Problem) error {
	var op db.DBOperation
	if p == nil {
		op = func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx,
				`DELETE FROM errors WHERE file_id = ? AND stage = ?`, fileID, db.StageVerify)
			return err
		}
	} else {
		kind := opStat
		if p.Kind == db.KindChecksumMismatch {
			kind = opHash
		}
		// The frames have to be taken here rather than on the writer's goroutine.
		err := db.WithStack(problemError(*p))
		op = func(ctx context.Context, tx *sqlx.Tx) error {
			return db.RecordError(ctx, tx, fileID, db.StageVerify, kind, err)
		}
	}
	if !database.Writer.Write(op) {
		return errors.New("database writer closed")
	}
	return nil
}

// forget deletes the records of placed files that are gone from the library:
// the registry row, and with it (ON DELETE CASCADE) the file's hash, plan and
// error rows. The database is backed up first, as a reset is — forgetting is
// the one thing a check writes that it cannot take back.
func forget(ctx context.Context, database *db.DB, outputDir string, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if err := database.Backup(ctx, filepath.Join(outputDir, db.BackupFileName)); err != nil {
		return fmt.Errorf("back up the database before forgetting missing files: %w", err)
	}
	return database.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		return db.Forget(ctx, tx, ids)
	})
}

// problemError turns a Problem back into an error carrying the sentinel its
// kind was derived from, so RecordError buckets it the same way every other
// stage's failures are bucketed rather than inventing a second table of kinds.
func problemError(p Problem) error {
	base := errors.New(p.Detail)
	switch p.Kind {
	case db.KindNotFound:
		return fmt.Errorf("%s: %w", p.Detail, fs.ErrNotExist)
	case db.KindChecksumMismatch:
		return fmt.Errorf("%s: %w", p.Detail, db.ErrChecksumMismatch)
	}
	return base
}

// checkDatabase asks SQLite whether the file holding the plan is sound. The
// live database is otherwise never checked — only the backup is, as it is
// written — so a corrupt page is found by whichever query happens to touch
// it. integrity_check, not quick_check: this is the deliberate, slow look,
// and quick_check skips exactly the index-against-table comparison that would
// catch a plan pointing at folders that are not there.
func checkDatabase(ctx context.Context, database *db.DB) (string, error) {
	var result string
	if err := database.SQL.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return "", fmt.Errorf("check database integrity: %w", err)
	}
	return result, nil
}

// strayPrefix is the name atomicfile.Copy gives a copy in flight.
const strayPrefix = ".copy-"

// findStrays walks the library for temp files a crashed transfer left behind.
// They are full-size copies of the user's photos sitting in the user's own
// folders under a dotted name, and nothing else in WanderSort ever collects
// them.
func findStrays(outputDir string) ([]string, error) {
	var strays []string
	err := filepath.WalkDir(outputDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner costs the sweep, never the run
		}
		if !d.IsDir() && strings.HasPrefix(d.Name(), strayPrefix) {
			if rel, relErr := filepath.Rel(outputDir, p); relErr == nil {
				strays = append(strays, wspath.ToLibrary(rel))
			}
		}
		return nil
	})
	return strays, err
}

func summary(rep Report, o Options, elapsed time.Duration) string {
	depth := "existence and size"
	if o.Full {
		depth = "contents"
	}
	msg := fmt.Sprintf("Checked the %s of %d files (%s) in %s",
		depth, rep.Checked, volume.HumanBytes(uint64(rep.Bytes)), elapsed)
	if len(rep.Forgotten) > 0 {
		msg += fmt.Sprintf(" — %d gone, forgotten", len(rep.Forgotten))
	}
	if len(rep.Problems) > 0 {
		msg += fmt.Sprintf(" — %d do not match", len(rep.Problems))
	}
	if rep.Database != "ok" {
		msg += " — the database itself is damaged"
	}
	return msg
}
