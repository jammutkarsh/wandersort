// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/location"
)

// TestConfigStampIgnoresNonPathSettings is the whole reason the stamp hashes a
// subset instead of the config file: a worker-count change cannot move a
// single file, so it must not read as a settings change.
func TestConfigStampIgnoresNonPathSettings(t *testing.T) {
	base := DefaultConfig()
	base.SavedPlaces = []string{"Indore"}

	other := base
	other.Workers = 16
	other.ClusterGap = 0
	other.Fallback = "Elsewhere"
	other.Anchors = []location.Anchor{{Name: "Indore"}}

	if ConfigStamp(base) != ConfigStamp(other) {
		t.Error("stamp changed on a setting that cannot change a target path")
	}
}

// TestConfigStampNilAndEmptyAgree keeps "no rules configured" from looking
// like a settings change depending on which layer produced it.
func TestConfigStampNilAndEmptyAgree(t *testing.T) {
	nilled := DefaultConfig()
	nilled.Rules, nilled.SavedPlaces = nil, nil
	empty := DefaultConfig()
	empty.Rules, empty.SavedPlaces = []string{}, []string{}

	if ConfigStamp(nilled) != ConfigStamp(empty) {
		t.Error("nil and empty slices stamped differently")
	}
}

func TestConfigStampChangesWithPathSettings(t *testing.T) {
	base := DefaultConfig()
	base.SavedPlaces = []string{"Indore"}

	for name, mutate := range map[string]func(*Config){
		"rules":          func(c *Config) { c.Rules = []string{RuleDevice} },
		"rules order":    func(c *Config) { c.Rules = []string{RuleLocation, RuleDate} },
		"collapse":       func(c *Config) { c.CollapseLevels = !c.CollapseLevels },
		"date only":      func(c *Config) { c.SavedPlacesDateOnly = !c.SavedPlacesDateOnly },
		"merge days":     func(c *Config) { c.MergeSameLocationDays = !c.MergeSameLocationDays },
		"saved places":   func(c *Config) { c.SavedPlaces = []string{"Indore", "Bhopal"} },
		"place spelling": func(c *Config) { c.SavedPlaces = []string{"Indore, Madhya Pradesh, India"} },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if ConfigStamp(base) == ConfigStamp(changed) {
				t.Errorf("%s changed but the stamp did not", name)
			}
		})
	}
}

func TestStampRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if _, ok, err := ReadStamp(dir); err != nil || ok {
		t.Fatalf("no stamp file: got ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	want := ConfigStamp(ConfigFor(&config.Configuration{Rules: []string{RuleDate}}))
	if err := WriteStamp(dir, want); err != nil {
		t.Fatalf("WriteStamp: %v", err)
	}
	got, ok, err := ReadStamp(dir)
	if err != nil || !ok {
		t.Fatalf("ReadStamp: got ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Errorf("stamp round trip: got %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, StampFileName)); err != nil {
		t.Errorf("stamp not written as %s: %v", StampFileName, err)
	}
}

// TestConfigForCopiesSavedPlaces guards the field the stamp reads: without it
// every library stamps as if it had no saved places.
func TestConfigForCopiesSavedPlaces(t *testing.T) {
	cfg := ConfigFor(&config.Configuration{SavedPlaces: []string{"Indore", "Bhopal"}})
	if len(cfg.SavedPlaces) != 2 {
		t.Errorf("SavedPlaces: got %v, want the configured two", cfg.SavedPlaces)
	}
}
