// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// runPlan builds the target path for each master under cfg, with no
// database and no geonames — Plan is a pure function of masters/labels/cfg.
func runPlan(t *testing.T, masters []masterFile, cfg Config) []masterFile {
	t.Helper()
	if err := Plan(context.Background(), masters, nil, cfg, nil, logger.NewNoopLogger()); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return masters
}

func TestPlanRulesOrderChangesSegmentOrder(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CollapseLevels = false

	cfg.Rules = []string{RuleDate, RuleLocation}
	dateFirst := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "a.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Goa"},
	}, cfg)
	if want := "2024/08_August/02/Goa/a.jpg"; dateFirst[0].targetPath != want {
		t.Errorf("date-first: got %q, want %q", dateFirst[0].targetPath, want)
	}

	cfg.Rules = []string{RuleLocation, RuleDate}
	locFirst := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "a.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Goa"},
	}, cfg)
	if want := "2024/08_August/Goa/02/a.jpg"; locFirst[0].targetPath != want {
		t.Errorf("location-first: got %q, want %q", locFirst[0].targetPath, want)
	}
}

func TestPlanCollapseLevelsDropsUniformDeviceOnly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDevice}
	cfg.CollapseLevels = true

	// one library-wide device value -> dropped
	same := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "a.jpg", DBDateTaken: new("2024:08:02 10:00:00"), DBModel: new("iPhone 13")},
		{FileDir: "/src", FileName: "b.jpg", DBDateTaken: new("2024:08:02 10:00:00"), DBModel: new("iPhone 13")},
	}, cfg)
	if want := "2024/08_August/a.jpg"; same[0].targetPath != want {
		t.Errorf("uniform device: got %q, want %q (device level should collapse)", same[0].targetPath, want)
	}

	// two distinct device values -> kept
	diff := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "a.jpg", DBDateTaken: new("2024:08:02 10:00:00"), DBModel: new("iPhone 13")},
		{FileDir: "/src", FileName: "b.jpg", DBDateTaken: new("2024:08:02 10:00:00"), DBModel: new("iPhone 14")},
	}, cfg)
	if want := "2024/08_August/iPhone-13/a.jpg"; diff[0].targetPath != want {
		t.Errorf("distinct device a: got %q, want %q (device level should stay)", diff[0].targetPath, want)
	}
	if want := "2024/08_August/iPhone-14/b.jpg"; diff[1].targetPath != want {
		t.Errorf("distinct device b: got %q, want %q (device level should stay)", diff[1].targetPath, want)
	}
}

func TestPlanSavedPlacesDateOnlySuppressesLocationSegment(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.SavedPlacesDateOnly = true
	cfg.CollapseLevels = false

	masters := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "home.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Indore", atSavedPlace: true},
		{FileDir: "/src", FileName: "trip.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Goa", atSavedPlace: false},
	}, cfg)

	if want := "2024/08_August/02/home.jpg"; masters[0].targetPath != want {
		t.Errorf("saved-place file: got %q, want %q (location folder should be suppressed)", masters[0].targetPath, want)
	}
	if want := "2024/08_August/02/Goa/trip.jpg"; masters[1].targetPath != want {
		t.Errorf("trip file: got %q, want %q (location folder should render)", masters[1].targetPath, want)
	}
}

func TestPlanMergeSameLocationDays(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.MergeSameLocationDays = true
	cfg.CollapseLevels = false

	// three consecutive Goa days merge into one range folder
	merged := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "d02.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "d03.jpg", DBDateTaken: new("2024:08:03 10:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "d04.jpg", DBDateTaken: new("2024:08:04 10:00:00"), location: "Goa"},
	}, cfg)
	for i, m := range merged {
		if want := "2024/08_August/02_04/Goa/" + m.FileName; m.targetPath != want {
			t.Errorf("merged day %d: got %q, want %q", i, m.targetPath, want)
		}
	}

	// a different-location day interleaved between two Goa days breaks the run
	interleaved := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "d02.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "d03.jpg", DBDateTaken: new("2024:08:03 10:00:00"), location: "Pune"},
		{FileDir: "/src", FileName: "d04.jpg", DBDateTaken: new("2024:08:04 10:00:00"), location: "Goa"},
	}, cfg)
	want := []string{"2024/08_August/02/Goa/d02.jpg", "2024/08_August/03/Pune/d03.jpg", "2024/08_August/04/Goa/d04.jpg"}
	for i, m := range interleaved {
		if m.targetPath != want[i] {
			t.Errorf("interleaved day %d: got %q, want %q (no merge across a different location)", i, m.targetPath, want[i])
		}
	}
}

func TestPlanScreenshotIgnoresRules(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDevice, RuleLocation, RuleOrientation, RuleMedia}
	cfg.CollapseLevels = false

	masters := runPlan(t, []masterFile{
		{
			FileDir: "/src", FileName: "shot.png",
			DBDateTaken: new("2024:08:02 10:00:00"), DBModel: new("iPhone 13"),
			location: "Goa", IsScreenshot: true,
		},
	}, cfg)
	if want := "2024/08_August/Screenshots/shot.png"; masters[0].targetPath != want {
		t.Errorf("screenshot: got %q, want %q (Rules must not fragment screenshots)", masters[0].targetPath, want)
	}
}
