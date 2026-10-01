package config

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
)

// Settings shape a library's folders. They live in the library's own
// database, so a library keeps the rules it was organized under. Stored as one
// JSON value: a new setting is a field here plus its default.
type Settings struct {
	// Rules are the ordered folder levels below Year/Month; empty keeps the
	// default pair. See vfs.Rule* for the names.
	Rules                 []string `json:"rules"`
	CollapseLevels        bool     `json:"collapseLevels"`
	SavedPlacesDateOnly   bool     `json:"savedPlacesDateOnly"`
	MergeSameLocationDays bool     `json:"mergeSameLocationDays"`
	// HomeTown and WorkTown are the user's everyday places as saved ("" =
	// none); both are anchored the same way.
	HomeTown string `json:"homeTown"`
	WorkTown string `json:"workTown"`
}

// DefaultSettings is what a brand-new library starts from.
func DefaultSettings() Settings {
	return Settings{
		CollapseLevels:        true,
		SavedPlacesDateOnly:   true,
		MergeSameLocationDays: true,
	}
}

// SavedPlaces is the distinct, non-empty everyday places to anchor.
func (s Settings) SavedPlaces() []string {
	var places []string
	for _, p := range []string{s.HomeTown, s.WorkTown} {
		if p != "" && (len(places) == 0 || places[0] != p) {
			places = append(places, p)
		}
	}
	return places
}

// Equal reports whether two settings would plan the same folders.
func (s Settings) Equal(o Settings) bool {
	a, errA := json.Marshal(s, json.Deterministic(true))
	b, errB := json.Marshal(o, json.Deterministic(true))
	return errA == nil && errB == nil && bytes.Equal(a, b)
}

// LoadSettings reads the library's settings. A library that has never been
// through the wizard has no row yet and gets the defaults; a setting the stored
// value predates keeps its default too.
func LoadSettings(ctx context.Context, database *db.DB) (Settings, error) {
	var stored string
	err := database.SQL.GetContext(ctx, &stored, `SELECT settings FROM library_settings WHERE id = 1`)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read library settings: %w", err)
	}
	s := DefaultSettings()
	if err := json.Unmarshal([]byte(stored), &s); err != nil {
		return Settings{}, fmt.Errorf("read library settings: %w", err)
	}
	return s, nil
}

// SaveSettings writes the library's settings, replacing whatever was there:
// the wizard always submits every setting, so there is nothing to merge.
func SaveSettings(ctx context.Context, database *db.DB, s Settings) error {
	encoded, err := json.Marshal(s, json.Deterministic(true))
	if err != nil {
		return fmt.Errorf("save library settings: %w", err)
	}
	if err := database.Writer.WriteSync(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO library_settings (id, settings) VALUES (1, ?)
			ON CONFLICT(id) DO UPDATE SET settings = excluded.settings`, string(encoded))
		return err
	}); err != nil {
		return fmt.Errorf("save library settings: %w", err)
	}
	return nil
}
