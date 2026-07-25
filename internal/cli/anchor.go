// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// syncHomeWorkFromConfig ensures the home/work anchors saved globally (see
// `wandersort config`) exist as ANCHOR_HOME/ANCHOR_WORK user_labels in this
// library's DB — anchors are a global setting, but resolveLocations reads them
// per-library, so each library's DB needs its own copy. Idempotent and silent
// once synced; a library with no global anchors set is a no-op.
func (a *App) syncHomeWorkFromConfig(ctx context.Context) error {
	g, err := config.LoadGlobal()
	if err != nil {
		a.Log.Warn("Could not read global config, skipping anchor sync", "error", err)
		return nil
	}
	if a.LocationResolver == nil {
		return nil
	}

	for _, anchor := range []struct{ name, kind string }{
		{g.HomeWork.Home, "ANCHOR_HOME"},
		{g.HomeWork.Work, "ANCHOR_WORK"},
	} {
		if anchor.name == "" {
			continue
		}
		var exists int
		if err := a.AppDB.SQL.GetContext(ctx, &exists,
			`SELECT COUNT(*) FROM user_labels WHERE kind = ? AND label = ?`, anchor.kind, anchor.name); err != nil {
			return fmt.Errorf("check anchor %q: %w", anchor.name, err)
		}
		if exists > 0 {
			continue
		}
		lat, lon, err := a.LocationResolver.ResolveByName(ctx, anchor.name)
		if err != nil {
			a.Log.Warn("Could not resolve saved anchor town", "town", anchor.name, "error", err)
			continue
		}
		if !a.AppDB.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO user_labels (label, kind, gps_lat, gps_lon) VALUES (?, ?, ?, ?)`,
				anchor.name, anchor.kind, lat, lon)
			return err
		}) {
			return fmt.Errorf("save anchor %q: writer closed", anchor.name)
		}
		a.Log.Info("Synced anchor for this library", logger.UserKey, true, "town", anchor.name, "kind", anchor.kind)
	}
	return nil
}

// exactMatch returns the gazetteer's own spelling when one of matches is a
// case-insensitive match for typed, e.g. so a user who already typed the
// right name gets the canonical form without extra steps (see canonicalTown).
// The town picker lists full names ("Indore, Madhya Pradesh, India"), so all
// three forms match — and whichever matched is saved as the full name, the
// only form that names one row for certain (see location.ResolveByName).
func exactMatch(matches []location.PlaceMatch, typed string) (string, bool) {
	typed = strings.TrimSpace(typed)
	for _, m := range matches {
		if strings.EqualFold(m.FullName, typed) {
			return canonicalNameOf(m), true
		}
	}
	for _, m := range matches {
		if strings.EqualFold(m.DisplayName, typed) || strings.EqualFold(m.Name, typed) {
			return canonicalNameOf(m), true
		}
	}
	return "", false
}

// canonicalNameOf is the form an anchor is saved as: the full name when the
// gazetteer gave one, else whatever shorter form it has.
func canonicalNameOf(m location.PlaceMatch) string {
	switch {
	case m.FullName != "":
		return m.FullName
	case m.DisplayName != "":
		return m.DisplayName
	default:
		return m.Name
	}
}
