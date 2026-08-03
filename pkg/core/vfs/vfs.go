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
	return New(database, resolver, log, cfg).Run(ctx)
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

	count, err := v.persist(masters)
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
			fm.exif_creation_date, fm.is_screenshot
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

// persist replaces the whole previous proposal — one live set for the
// library. The delete goes through the same FIFO writer as the inserts, so a
// rebuild leaves no stale rows behind.
func (v *VFS) persist(masters []masterFile) (int, error) {
	if !v.db.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM virtual_fs_entries`)
		return err
	}) {
		return 0, fmt.Errorf("clear previous vfs proposal: writer closed")
	}
	for chunk := range slices.Chunk(masters, insertChunk) {
		stmt, args := insertStatement(chunk)
		if !v.db.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
				return fmt.Errorf("persist %d vfs entries from file %d: %w", len(chunk), chunk[0].FileID, err)
			}
			return nil
		}) {
			return 0, fmt.Errorf("persist vfs entries: writer closed")
		}
	}
	return len(masters), nil
}

// insertChunk is how many proposals go into one INSERT. A long VALUES list is
// expensive for SQLite to compile, so this is a measured trough over 20k rows
// (1 row 504ms, 50 rows 241ms, 500 rows 486ms), not "bigger is better".
const insertChunk = 50

// insertStatement builds one parameterised multi-row INSERT for a chunk, so
// SQLite compiles the statement once instead of once per proposal.
func insertStatement(chunk []masterFile) (string, []any) {
	var stmt strings.Builder
	stmt.WriteString(`INSERT INTO virtual_fs_entries
		(file_id, source_path, target_path, cluster_id, status, location_dir)
		VALUES `)
	args := make([]any, 0, len(chunk)*6)
	for i := range chunk {
		if i > 0 {
			stmt.WriteString(",")
		}
		stmt.WriteString("(?,?,?,?,?,?)")
		m := &chunk[i]
		args = append(args, m.FileID, m.absPath, m.targetPath,
			nullable(m.clusterID), db.StatusProposed, nullable(m.locationDir))
	}
	return stmt.String(), args
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
