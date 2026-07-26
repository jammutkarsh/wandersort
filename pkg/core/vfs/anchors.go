// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// SyncAnchors ensures the home/work towns saved globally (see `wandersort
// config`) exist as ANCHOR_HOME/ANCHOR_WORK user_labels in this library's DB —
// anchors are a global setting, but resolveLocations reads them per-library, so
// each library's DB needs its own copy. Idempotent and silent once synced;
// empty names are a no-op.
//
// It lives here rather than in the CLI because resolveLocations is the only
// thing that reads these rows: the phase that consumes an anchor owns writing
// it. The caller supplies the names, so this package never reads the user's
// config file.
func SyncAnchors(ctx context.Context, d *db.DB, resolver *location.Resolver, log logger.Logger, home, work string) error {
	if resolver == nil {
		return nil
	}

	for _, anchor := range []struct{ name, kind string }{
		{home, "ANCHOR_HOME"},
		{work, "ANCHOR_WORK"},
	} {
		if anchor.name == "" {
			continue
		}
		var exists int
		if err := d.SQL.GetContext(ctx, &exists,
			`SELECT COUNT(*) FROM user_labels WHERE kind = ? AND label = ?`, anchor.kind, anchor.name); err != nil {
			return fmt.Errorf("check anchor %q: %w", anchor.name, err)
		}
		if exists > 0 {
			continue
		}
		lat, lon, err := resolver.ResolveByName(ctx, anchor.name)
		if err != nil {
			log.Warn("Could not resolve saved anchor town", "town", anchor.name, "error", err)
			continue
		}
		if !d.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO user_labels (label, kind, gps_lat, gps_lon) VALUES (?, ?, ?, ?)`,
				anchor.name, anchor.kind, lat, lon)
			return err
		}) {
			return fmt.Errorf("save anchor %q: writer closed", anchor.name)
		}
		log.Info("Synced anchor for this library", logger.UserKey, true, "town", anchor.name, "kind", anchor.kind)
	}
	return nil
}
