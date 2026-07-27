// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package vfs is phase 4 of the pipeline. It proposes a destination folder
// hierarchy for every master file in the library — regardless of which scan
// session indexed it — without touching anything on disk, persisting the
// proposal as PROPOSED rows in virtual_fs_entries. Each run replaces the
// previous proposal wholesale, so the same set of source files always yields
// the same proposal. The review flow (issue #8) approves or corrects it; a
// future Execute phase performs the actual copy/move.
package vfs

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/jmoiron/sqlx"

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

	labels, err := v.loadLabels(ctx)
	if err != nil {
		return 0, err
	}

	if err := Plan(ctx, masters, labels, v.cfg, v.resolver, v.log); err != nil {
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

func (v *VFS) loadLabels(ctx context.Context) ([]userLabel, error) {
	var labels []userLabel
	if err := v.db.SQL.SelectContext(ctx, &labels,
		`SELECT label, kind, time_start, time_end, gps_lat, gps_lon FROM user_labels`); err != nil {
		return nil, fmt.Errorf("query user labels: %w", err)
	}
	return labels, nil
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
	for i := range masters {
		m := masters[i]
		if !v.db.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO virtual_fs_entries
					(file_id, source_path, target_path, cluster_id, status, suggestion, suggestion_source, suggestion_dir)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				m.FileID, m.absPath, m.targetPath,
				nullable(m.clusterID), db.StatusProposed,
				nullable(m.suggestion), nullable(m.suggestionSource), nullable(m.suggestionDir)); err != nil {
				return fmt.Errorf("persist vfs entry for file %d: %w", m.FileID, err)
			}
			return nil
		}) {
			return i, fmt.Errorf("persist vfs entry for file %d: writer closed", m.FileID)
		}
	}
	return len(masters), nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
