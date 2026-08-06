// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// StampFileName records, in the output directory, the settings the current
// proposal was built under. One output folder has exactly one rule set, so the
// stamp is that folder's identity — reusable across re-scans and new source
// folders, which is why it isn't a database row.
const StampFileName = ".wandersort.cfg"

// ConfigStamp is a fingerprint of the settings that can change a target path:
// Rules, the three folder toggles, and the saved-place names.
//
// Deliberately not the whole config file: hashing raw bytes would make
// `workers: 8→16` — which cannot move a single file — look like a settings
// change and prompt for a rebuild. Saved places are hashed as the names the
// user typed, not as resolved anchors, so the check never needs the location
// database.
func ConfigStamp(cfg Config) string {
	// A fixed struct marshals its fields in declaration order, so this is
	// canonical without sorting anything.
	stamped := struct {
		Rules                 []string `json:"rules"`
		CollapseLevels        bool     `json:"collapse_levels"`
		SavedPlacesDateOnly   bool     `json:"saved_places_date_only"`
		MergeSameLocationDays bool     `json:"merge_same_location_days"`
		SavedPlaces           []string `json:"saved_places"`
	}{
		Rules:                 cfg.Rules,
		CollapseLevels:        cfg.CollapseLevels,
		SavedPlacesDateOnly:   cfg.SavedPlacesDateOnly,
		MergeSameLocationDays: cfg.MergeSameLocationDays,
		SavedPlaces:           cfg.SavedPlaces,
	}
	// nil and empty are the same setting; JSON spells them differently.
	if stamped.Rules == nil {
		stamped.Rules = []string{}
	}
	if stamped.SavedPlaces == nil {
		stamped.SavedPlaces = []string{}
	}
	raw, err := json.Marshal(stamped)
	if err != nil { // only strings and bools — unreachable
		return ""
	}
	sum := sha256.Sum256(raw)
	return base64.RawStdEncoding.EncodeToString(sum[:])
}

// WriteStamp records the settings a fresh proposal was built under.
func WriteStamp(outputDir, stamp string) error {
	if err := os.WriteFile(filepath.Join(outputDir, StampFileName), []byte(stamp+"\n"), 0o644); err != nil {
		return fmt.Errorf("write config stamp: %w", err)
	}
	return nil
}

// ReadStamp returns the stamp the current proposal was built under. ok is
// false when there is no stamp file — a proposal from before stamping, which
// must never be reported as a settings change.
func ReadStamp(outputDir string) (stamp string, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(outputDir, StampFileName))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read config stamp: %w", err)
	}
	return strings.TrimSpace(string(data)), true, nil
}
