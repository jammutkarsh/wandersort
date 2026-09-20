// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jammutkarsh/wandersort/pkg/db"
)

// Settings are the settings that shape a library's folders. They live in that
// library's own database (spec D2), so a second scan into the same folder uses
// the rules that folder was organized under, whatever another library says.
type Settings struct {
	// Rules are the ordered folder levels below Year/Month; empty keeps the
	// default pair. See vfs.Rule* for the names.
	Rules                 []string
	CollapseLevels        bool
	SavedPlacesDateOnly   bool
	MergeSameLocationDays bool
	// SavedPlaces is positional: index 0 is home, 1 is work, everything else
	// is another frequently-stayed-at place — all anchored the same way.
	SavedPlaces []string
}

// DefaultSettings is what a brand-new library starts from.
func DefaultSettings() Settings {
	return Settings{
		CollapseLevels:        true,
		SavedPlacesDateOnly:   true,
		MergeSameLocationDays: true,
	}
}

// Equal reports whether two settings would plan the same folders. The test
// behind "did this save change anything": a wizard visit that changes nothing
// must not throw a plan away.
func (s Settings) Equal(o Settings) bool {
	return s.CollapseLevels == o.CollapseLevels &&
		s.SavedPlacesDateOnly == o.SavedPlacesDateOnly &&
		s.MergeSameLocationDays == o.MergeSameLocationDays &&
		slices.Equal(s.Rules, o.Rules) &&
		slices.Equal(s.SavedPlaces, o.SavedPlaces)
}

// settingsRow is the one row of library_settings, as stored: the two lists
// are JSON, since nothing queries inside them.
type settingsRow struct {
	Rules                 string `db:"rules"`
	CollapseLevels        bool   `db:"collapse_levels"`
	SavedPlacesDateOnly   bool   `db:"saved_places_date_only"`
	MergeSameLocationDays bool   `db:"merge_same_location_days"`
	SavedPlaces           string `db:"saved_places"`
}

// LoadSettings reads the library's settings. A library that has never been
// through the wizard has no row yet and gets the defaults from code.
func LoadSettings(ctx context.Context, database *db.DB) (Settings, error) {
	var row settingsRow
	err := database.SQL.GetContext(ctx, &row, `SELECT rules, collapse_levels,
		saved_places_date_only, merge_same_location_days, saved_places
		FROM library_settings WHERE id = 1`)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read library settings: %w", err)
	}
	s := Settings{
		CollapseLevels:        row.CollapseLevels,
		SavedPlacesDateOnly:   row.SavedPlacesDateOnly,
		MergeSameLocationDays: row.MergeSameLocationDays,
	}
	if err := json.Unmarshal([]byte(row.Rules), &s.Rules); err != nil {
		return Settings{}, fmt.Errorf("read library settings: rules: %w", err)
	}
	if err := json.Unmarshal([]byte(row.SavedPlaces), &s.SavedPlaces); err != nil {
		return Settings{}, fmt.Errorf("read library settings: saved places: %w", err)
	}
	return s, nil
}

// SaveSettings writes the library's settings, replacing whatever was there:
// the wizard always submits every setting, so there is nothing to merge.
func SaveSettings(ctx context.Context, database *db.DB, s Settings) error {
	rules, err := json.Marshal(nonNil(s.Rules))
	if err != nil {
		return fmt.Errorf("save library settings: %w", err)
	}
	places, err := json.Marshal(nonNil(s.SavedPlaces))
	if err != nil {
		return fmt.Errorf("save library settings: %w", err)
	}
	if _, err := database.SQL.ExecContext(ctx, `INSERT INTO library_settings
		(id, rules, collapse_levels, saved_places_date_only, merge_same_location_days, saved_places)
		VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET rules = excluded.rules,
			collapse_levels = excluded.collapse_levels,
			saved_places_date_only = excluded.saved_places_date_only,
			merge_same_location_days = excluded.merge_same_location_days,
			saved_places = excluded.saved_places`,
		string(rules), s.CollapseLevels, s.SavedPlacesDateOnly, s.MergeSameLocationDays, string(places)); err != nil {
		return fmt.Errorf("save library settings: %w", err)
	}
	return nil
}

// nonNil keeps a nil slice from being stored as JSON null — nil and empty are
// the same setting, and one spelling is enough.
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
