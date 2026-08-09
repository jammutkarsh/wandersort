// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package vfs is the pipeline's final phase: it proposes a destination folder
// hierarchy for every master file in the library, persisted as PROPOSED rows
// in virtual_fs_entries, without touching anything on disk. The review flow
// approves or corrects it; a future Execute phase performs the copy/move.
package vfs

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

type VFS struct {
	db       *db.DB
	resolver *location.Resolver
	log      logger.Logger
	cfg      Config
}

func New(db *db.DB, resolver *location.Resolver, log logger.Logger, cfg Config) *VFS {
	return &VFS{
		db:       db,
		resolver: resolver,
		log:      log,
		cfg:      cfg,
	}
}

// Propose builds the proposal for the whole library from the user's settings —
// the phase as a single call, for every caller that has an *config.Configuration
// and a resolver (the scan pipeline, and review --rebuild). Assembling the
// Config and resolving the saved-place anchors are steps of the phase, not of
// its callers; New is for a test or a caller that wants to state the Config
// itself.
func Propose(ctx context.Context, database *db.DB, resolver *location.Resolver, appCfg *config.Configuration, log logger.Logger) (int, error) {
	cfg := ConfigFor(appCfg)
	cfg.Anchors = resolver.BuildAnchors(ctx, appCfg.SavedPlaces)
	log.Info("Proposing destination folders", "rules", cfg.Rules, "anchors", len(cfg.Anchors))
	count, err := New(database, resolver, log, cfg).Run(ctx)
	if err != nil {
		return count, err
	}
	// Record what this proposal was built under, so the review can tell that
	// the settings moved since. Here rather than in each caller: every path to
	// a fresh proposal goes through Propose, and only Propose knows both the
	// Config and the output directory.
	if appCfg.AppDBPath != "" {
		if err := WriteStamp(filepath.Dir(appCfg.AppDBPath), ConfigStamp(cfg)); err != nil {
			// A missing stamp only costs a rebuild prompt that never fires —
			// not worth failing a finished proposal over.
			log.Warn("Could not record the settings this proposal used", "error", err)
		}
	}
	return count, nil
}

// Run builds the virtual filesystem proposal for the whole library's master
// files
func (v *VFS) Run(ctx context.Context) (int, error) {
	v.log.Info("Building virtual filesystem")

	masters, err := v.loadMasters(ctx)
	if err != nil {
		return 0, err
	}
	if len(masters) == 0 {
		v.log.Info("No master files to organize")
		return 0, nil
	}

	if err := Plan(ctx, masters, v.cfg, v.resolver, v.log); err != nil {
		return 0, err
	}

	count, err := v.persist(ctx, masters)
	if err != nil {
		return count, err
	}

	v.log.Info("Virtual filesystem proposed", "entries", count)
	return count, nil
}

// loadMasters reads every live master in the library with its hashed metadata.
// Not session-scoped: the proposal must cover earlier sessions' files too, or
// the output would depend on scan history. Ordered by (file_dir, file_name),
// not id, so clustering and collision suffixes don't vary with worker order.
func (v *VFS) loadMasters(ctx context.Context) ([]masterFile, error) {
	var masters []masterFile
	if err := v.db.SQL.SelectContext(ctx, &masters, `
		SELECT fr.id, fr.file_dir, fr.file_name, fr.media_type, fr.file_extension, fr.file_modified_at,
			fm.exif_image_width, fm.exif_image_height, fm.exif_orientation,
			fm.exif_gps_latitude, fm.exif_gps_longitude,
			fm.exif_make, fm.exif_model, fm.exif_date_time_original, fm.exif_create_date,
			fm.exif_creation_date, fm.exif_media_create_date, fm.is_screenshot
		FROM live_files fr
		JOIN file_metadata fm ON fm.file_id = fr.id
		WHERE fm.is_master = 1
		ORDER BY fr.file_dir, fr.file_name`); err != nil {
		return nil, fmt.Errorf("query master files: %w", err)
	}
	for i := range masters {
		masters[i].absPath = filepath.Join(masters[i].FileDir, masters[i].FileName)
	}
	return masters, nil
}

// persist replaces the still-proposed part of the library's plan, and leaves
// everything the reviewer already signed off alone: a rebuild re-proposes what
// nobody has decided yet, not what they decided. A kept row whose file is no
// longer a live master goes, or the plan would keep promising to move a file
// that isn't there.
//
// The deletes go through the same FIFO writer as the inserts, so a rebuild
// leaves no stale rows behind.
func (v *VFS) persist(ctx context.Context, masters []masterFile) (int, error) {
	// Read the survivors first: the writer is an asynchronous FIFO, so a query
	// issued after queueing the deletes would still see the rows they remove.
	// A decided row is one that is approved or already executed; its file must
	// not be proposed a second time — UNIQUE(file_id) says so too.
	var keptIDs []int64
	if err := v.db.SQL.SelectContext(ctx, &keptIDs, `
		SELECT file_id FROM virtual_fs_entries
		WHERE status != ? AND file_id IN (
			SELECT fr.id FROM live_files fr
			JOIN file_metadata fm ON fm.file_id = fr.id
			WHERE fm.is_master = 1)`, db.StatusProposed); err != nil {
		return 0, fmt.Errorf("load decided vfs entries: %w", err)
	}
	kept := make(map[int64]bool, len(keptIDs))
	for _, id := range keptIDs {
		kept[id] = true
	}

	if !v.db.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM virtual_fs_entries WHERE status = ?`, db.StatusProposed); err != nil {
			return err
		}
		// a decided row for a file that is no longer a live master promises a
		// move that can't happen
		_, err := tx.ExecContext(ctx, `
			DELETE FROM virtual_fs_entries
			WHERE status != ? AND file_id NOT IN (
				SELECT fr.id FROM live_files fr
				JOIN file_metadata fm ON fm.file_id = fr.id
				WHERE fm.is_master = 1)`, db.StatusProposed)
		return err
	}) {
		return 0, fmt.Errorf("clear previous vfs proposal: writer closed")
	}

	for chunk := range slices.Chunk(masters, insertChunk) {
		stmt, args, n := insertStatement(chunk, kept)
		if n == 0 {
			continue
		}
		if !v.db.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
				return fmt.Errorf("persist %d vfs entries from file %d: %w", n, chunk[0].FileID, err)
			}
			return nil
		}) {
			return 0, fmt.Errorf("persist vfs entries: writer closed")
		}
	}
	// The writer is an asynchronous FIFO, so returning here only means the
	// rows are *queued*. Every caller reads them back immediately — the review
	// rebuilds its tree the moment Propose returns — and without this the read
	// races the batch and shows the proposal this run just replaced.
	v.db.Writer.Flush()
	// every live master now has exactly one entry: freshly proposed, or kept
	return len(masters), nil
}

// insertChunk is how many proposals go into one INSERT. A long VALUES list is
// expensive for SQLite to compile, so this is a measured trough over 20k rows
// (1 row 504ms, 50 rows 241ms, 500 rows 486ms), not "bigger is better".
const insertChunk = 50

// insertStatement builds one parameterised multi-row INSERT for a chunk, so
// SQLite compiles the statement once instead of once per proposal. Masters in
// kept already hold a decided entry and are skipped; n is how many rows the
// statement actually inserts (0 = nothing to run).
func insertStatement(chunk []masterFile, kept map[int64]bool) (stmt string, args []any, n int) {
	var b strings.Builder
	b.WriteString(`INSERT INTO virtual_fs_entries
		(file_id, source_path, target_path, cluster_id, status, location_dir, taken_at)
		VALUES `)
	args = make([]any, 0, len(chunk)*7)
	for i := range chunk {
		m := &chunk[i]
		if kept[m.FileID] {
			continue
		}
		if n > 0 {
			b.WriteString(",")
		}
		b.WriteString("(?,?,?,?,?,?,?)")
		args = append(args, m.FileID, m.absPath, m.targetPath,
			nullable(m.clusterID), db.StatusProposed, nullable(m.locationDir),
			nullableTime(m.folderTime()))
		n++
	}
	return b.String(), args, n
}

// nullableTime stores a folder date in the canonical fixed-width UTC form, or
// NULL for a file with no date at all (which is its own review segment).
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return db.FormatTime(t)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
