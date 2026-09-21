// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// buildSettingsForm builds the wizard's fields (seeded with the library's
// current settings) and a save closure that writes them back to it.
//
// The output folder is asked for only while there is still a choice: once the
// library is open the database and the lock are on it, and a library's folder
// never moves (spec D5).
func (a *app) buildSettingsForm(ctx context.Context, geonames func() (*location.Resolver, error)) ([]*tui.Field, func() error) {
	out := a.Config.OutputDir()
	groupBy := append([]string{}, a.Config.Rules...)
	if len(groupBy) == 0 {
		groupBy = []string{vfs.RuleDate, vfs.RuleLocation} // sensible default
	}
	collapse := a.Config.CollapseLevels
	mergeDays := a.Config.MergeSameLocationDays
	spDateOnly := a.Config.SavedPlacesDateOnly
	home, work := "", ""
	if places := a.Config.SavedPlaces; len(places) > 0 {
		home = places[0]
		if len(places) > 1 {
			work = places[1]
		}
	}

	// Rejects a typo (close candidates exist) but waves through an unknown name
	// or a geonames database that never opened — a broken dependency can't trap this field.
	townValidator := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return nil // blank = skip
		}
		resolver, err := geonames()
		if err != nil {
			if errors.Is(err, install.ErrPending) {
				return err
			}
			return nil
		}
		_, err = resolver.Canonical(ctx, s)
		return err
	}

	// same rule at save time: the geonames spelling when it can give one,
	// else what was typed — dropping a town the user already had is data loss
	canonicalTownOrTyped := func(typed string) string {
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return ""
		}
		resolver, err := geonames()
		if err != nil {
			return typed // pending or broken geonames — never drop what was typed
		}
		if name, err := resolver.Canonical(ctx, typed); err == nil {
			return name
		}
		return "" // near-miss the validator would have rejected ("did you mean")
	}

	paths := path.New()
	homeDir := paths.HomeDir

	// The libraries this machine has opened come first: picking one again is
	// far more common than starting a third (spec D3). Then locations under
	// folders that exist on this machine — ~/Pictures is a macOS/Windows
	// convention; a Linux box without it won't offer it.
	var outSuggestions []string
	for _, dir := range a.Config.History() {
		outSuggestions = append(outSuggestions, paths.RelativeToHome(dir))
	}
	for _, c := range []string{
		filepath.Join(homeDir, "Pictures", "WanderSort"),
		filepath.Join(homeDir, "WandersortLibrary"),
	} {
		if st, err := os.Stat(filepath.Dir(c)); err == nil && st.IsDir() {
			outSuggestions = append(outSuggestions, paths.RelativeToHome(c))
		}
	}
	// suggestOut is the shared directory completion plus this field's own
	// seed suggestions for an empty input.
	suggestOut := func(typed string) []string {
		if strings.TrimSpace(typed) == "" {
			return outSuggestions
		}
		return suggestDirs(paths, typed)
	}

	suggestTown := func(typed string) []string {
		typed = strings.TrimSpace(typed)
		resolver, err := geonames()
		if len(typed) < 2 || err != nil {
			return nil
		}
		return resolver.SuggestNames(ctx, typed, 6)
	}

	rulesField := &tui.Field{
		Kind:     tui.FieldMultiSelect,
		Title:    "Rules",
		Options:  []string{vfs.RuleDate, vfs.RuleLocation, vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia},
		Selected: toMap(groupBy),
		Description: "Folder levels below Year/Month, in nesting order.\n" +
			"  date = day    location = city    device = camera\n" +
			"  orientation = portrait/landscape    media = photo/video",
	}
	ex := newSettingsExamples(rulesField, &collapse, &mergeDays, &spDateOnly, &home)
	rulesField.Example = ex.Rules

	fields := []*tui.Field{rulesField}
	// Asked first, and only while the answer can still change anything: an
	// open library already has its database and lock on one folder.
	if a.AppDB == nil {
		fields = append([]*tui.Field{{
			Kind:        tui.FieldInput,
			Title:       "Output path",
			Description: "Where the organized library goes: an empty folder, or one WanderSort already organized. ~ is fine.",
			Value:       &out,
			Placeholder: filepath.Join(homeDir, config.DefaultLibrary),
			Suggest:     suggestOut,
			Validator: func(s string) error {
				return config.CheckLibrary(paths.ExpandPath(strings.TrimSpace(s)))
			},
		}}, fields...)
	}
	fields = append(fields,
		&tui.Field{
			Kind:        tui.FieldGroup,
			Title:       "Saved places",
			Description: "The everyday places you shoot from, and how their photos are foldered.",
			// Await blocks the form until the location database finishes downloading.
			Await: func() string {
				if _, err := geonames(); errors.Is(err, install.ErrPending) {
					return "Waiting for the location database to finish downloading…"
				}
				return ""
			},
			Subs: []*tui.Field{
				{
					Kind: tui.FieldInput, Title: "Home town", Placeholder: "e.g. Delhi (blank to skip)",
					Value: &home, Validator: townValidator, Suggest: suggestTown,
				},
				{
					Kind: tui.FieldInput, Title: "Work town", Placeholder: "blank = same as home",
					Value: &work, Validator: townValidator, Suggest: suggestTown,
				},
				{
					Kind:      tui.FieldConfirm,
					Title:     "Collapse uninformative levels?",
					Describe:  ex.CollapseDescribe,
					BoolValue: &collapse,
					Example:   ex.Collapse,
				},
				{
					Kind:      tui.FieldConfirm,
					Title:     "Group saved-place photos by date only?",
					Describe:  ex.DateOnlyDescribe,
					BoolValue: &spDateOnly,
					Example:   ex.DateOnly,
				},
				{
					Kind:      tui.FieldConfirm,
					Title:     "Merge consecutive same-location days?",
					Describe:  ex.MergeDaysDescribe,
					BoolValue: &mergeDays,
					Example:   ex.MergeDays,
				},
			},
		},
	)

	// save runs after the form completes. It reads the bound vars this form
	// wrote and writes them to the library they belong to — opening (or
	// creating) it here if the output path above is how this session picked
	// one, so quitting before the save writes nothing anywhere.
	save := func() error {
		if strings.TrimSpace(work) == "" {
			work = home // blank work = same as home
		}
		// Collect multiselect choices from the map, in canonical option order.
		var selectedRules []string
		for _, opt := range rulesField.Options {
			if rulesField.Selected[opt] {
				selectedRules = append(selectedRules, opt)
			}
		}
		s := config.Settings{
			Rules:                 selectedRules,
			CollapseLevels:        collapse,
			SavedPlacesDateOnly:   spDateOnly,
			MergeSameLocationDays: mergeDays,
			// Canonicalize towns to the exact geonames spelling before saving.
			SavedPlaces: []string{canonicalTownOrTyped(home), canonicalTownOrTyped(work)},
		}
		if err := a.saveSettings(ctx, paths.ExpandPath(strings.TrimSpace(out)), s); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
		return nil
	}
	return fields, save
}
