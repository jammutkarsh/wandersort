// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"path"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
)

// coordinates the geocoder names "Panjim, India" and "New Delhi"
var (
	panji = [2]float64{15.4909, 73.8278}
	delhi = [2]float64{28.6139, 77.2090}
)

// placeFile seeds one placed file at target, creating (or reusing by name)
// its folders top first with the given levels and bounds, and returns the
// folder it sits in.
func (h *harness) placeFile(t *testing.T, meta classifier.CommonMetadata, target string, levels []string, bounds []Bounds) int64 {
	t.Helper()
	ctx := context.Background()
	id := h.addFile(t, "lib/"+path.Base(target), classifier.MediaTypeImage, meta)
	if _, err := h.d.ExecContext(ctx, `UPDATE file_registry SET placed = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var parent int64
	for i, name := range strings.Split(path.Dir(target), "/") {
		var folder int64
		err := h.d.QueryRowContext(ctx, `SELECT id FROM folder_nodes WHERE parent_id IS ? AND name = ?`,
			nullableID(parent), name).Scan(&folder)
		if err != nil {
			res, err := h.d.ExecContext(ctx, `INSERT INTO folder_nodes (parent_id, name, level, bounds) VALUES (?, ?, ?, ?)`,
				nullableID(parent), name, levels[i], bounds[i])
			if err != nil {
				t.Fatal(err)
			}
			if folder, err = res.LastInsertId(); err != nil {
				t.Fatal(err)
			}
		}
		parent = folder
	}
	if _, err := h.d.ExecContext(ctx, `INSERT INTO virtual_fs_entries (file_id, source_path, node_id, target_path, status)
		VALUES (?, ?, ?, ?, ?)`, id, target, parent, target, db.StatusDone); err != nil {
		t.Fatal(err)
	}
	return parent
}

// placedGoaTrip is a reviewed and copied trip: three places over three days,
// merged and renamed to Goa Trip.
func placedGoaTrip(t *testing.T, h *harness) int64 {
	return h.placeFile(t, metaWith("2024:03:01 10:00:00", panji[0], panji[1], 3024, 4032),
		"2024/03_March/01_03/Goa Trip/IMG_0001.HEIC",
		[]string{LevelYear, LevelMonth, RuleDate, RuleLocation},
		[]Bounds{
			{{Year: []int{2024}}},
			{{Month: []int{3}}},
			{{Date: []int{1, 2, 3}}},
			{{Location: []string{"Baga", "Mumbai", "Panjim, India"}}},
		})
}

func dateLocation() Config {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	return cfg
}

func TestRouteIntoPlacedFolder(t *testing.T) {
	for _, tc := range []struct {
		name, dto string
		at        [2]float64
		want      string
	}{
		{"matching place and day joins the trip", "2024:03:02 10:00:00", panji, "2024/03_March/01_03/Goa Trip/NEW.HEIC"},
		{"another place that day gets its own day", "2024:03:02 10:00:00", delhi, "2024/03_March/02/New-Delhi/NEW.HEIC"},
		{"the trip's place on another day", "2024:03:05 10:00:00", panji, "2024/03_March/05/Panjim-India/NEW.HEIC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tripFolder := placedGoaTrip(t, h)
			id := h.addFile(t, "dump/NEW.HEIC", classifier.MediaTypeImage, metaWith(tc.dto, tc.at[0], tc.at[1], 3024, 4032))
			got := h.build(t, dateLocation(), installtest.Resolver(t))
			if got[id].TargetPath != tc.want {
				t.Errorf("target = %q, want %q", got[id].TargetPath, tc.want)
			}
			// the placed folder itself is never handed to a new file: the review
			// could rename it, and the placed file stays where it was copied
			var node int64
			if err := h.d.SQL.Get(&node, `SELECT node_id FROM virtual_fs_entries WHERE file_id = ?`, id); err != nil {
				t.Fatal(err)
			}
			if node == tripFolder {
				t.Errorf("new file sits in the placed folder %d, want a folder of its own", node)
			}
		})
	}
}

// A dropped day folder can come back for new data (spec D15).
func TestRouteAfterDrop(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   [2]float64
		want string
	}{
		{"the dropped day's place joins it", panji, "2024/03_March/Panjim-India/NEW.HEIC"},
		{"another place recreates the day", delhi, "2024/03_March/12/New-Delhi/NEW.HEIC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			// `12` dropped over Panji: its day pushed into the place
			h.placeFile(t, metaWith("2024:03:12 10:00:00", panji[0], panji[1], 3024, 4032),
				"2024/03_March/Panjim-India/IMG_0001.HEIC",
				[]string{LevelYear, LevelMonth, RuleLocation},
				[]Bounds{{{Year: []int{2024}}}, {{Month: []int{3}}}, {{Date: []int{12}, Location: []string{"Panjim, India"}}}})
			id := h.addFile(t, "dump/NEW.HEIC", classifier.MediaTypeImage,
				metaWith("2024:03:12 18:00:00", tc.at[0], tc.at[1], 3024, 4032))
			if got := h.build(t, dateLocation(), installtest.Resolver(t))[id].TargetPath; got != tc.want {
				t.Errorf("target = %q, want %q", got, tc.want)
			}
		})
	}
}

// Rules changed between batches (location off): the placed file stays, the
// new one follows the new rules (spec D6).
func TestRouteAfterRulesChange(t *testing.T) {
	h := newHarness(t)
	const placedPath = "2024/03_March/02/New-Delhi/IMG_0001.HEIC"
	h.placeFile(t, metaWith("2024:03:02 10:00:00", delhi[0], delhi[1], 3024, 4032), placedPath,
		[]string{LevelYear, LevelMonth, RuleDate, RuleLocation},
		[]Bounds{{{Year: []int{2024}}}, {{Month: []int{3}}}, {{Date: []int{2}}}, {{Location: []string{"New Delhi"}}}})
	id := h.addFile(t, "dump/NEW.HEIC", classifier.MediaTypeImage, metaWith("2024:03:02 18:00:00", delhi[0], delhi[1], 3024, 4032))

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate}
	got := h.build(t, cfg, installtest.Resolver(t))
	if got[id].TargetPath != "2024/03_March/02/NEW.HEIC" {
		t.Errorf("new file = %q, want 2024/03_March/02/NEW.HEIC", got[id].TargetPath)
	}
	if got[1].TargetPath != placedPath || got[1].Status != db.StatusDone {
		t.Errorf("placed file = %+v, want it untouched at %s", got[1], placedPath)
	}
}

// A photo just after midnight continuing a placed New Year's Eve lands in
// December with it (spec D16).
func TestRouteContinuesPlacedCluster(t *testing.T) {
	h := newHarness(t)
	h.placeFile(t, metaWith("2024:12:31 23:50:00", 0, 0, 3024, 4032), "2024/12_December/31/IMG_0001.HEIC",
		[]string{LevelYear, LevelMonth, RuleDate},
		[]Bounds{{{Year: []int{2024}}}, {{Month: []int{12}}}, {{Date: []int{31}}}})
	id := h.addFile(t, "dump/NEW.HEIC", classifier.MediaTypeImage, metaWith("2025:01:01 00:30:00", 0, 0, 3024, 4032))

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate}
	if got := h.build(t, cfg, nil)[id].TargetPath; got != "2024/12_December/Jan_01/NEW.HEIC" {
		t.Errorf("target = %q, want it under 2024/12_December", got)
	}
}

// Planning again with nothing new proposes nothing, and a routed batch
// planned twice lands the same way.
func TestRouteReplan(t *testing.T) {
	h := newHarness(t)
	placedGoaTrip(t, h)
	cfg, geo := dateLocation(), installtest.Resolver(t)
	h.build(t, cfg, geo)
	if n := proposedCount(t, h); n != 0 {
		t.Fatalf("%d proposed rows with no new files, want 0", n)
	}

	id := h.addFile(t, "dump/NEW.HEIC", classifier.MediaTypeImage, metaWith("2024:03:02 10:00:00", panji[0], panji[1], 3024, 4032))
	first := h.build(t, cfg, geo)[id]
	folders := folderCount(t, h.d)
	second := h.build(t, cfg, geo)[id]
	if first.TargetPath != second.TargetPath || folderCount(t, h.d) != folders {
		t.Errorf("second plan %+v (%d folders), want %+v (%d folders)", second, folderCount(t, h.d), first, folders)
	}
	if n := proposedCount(t, h); n != 1 {
		t.Errorf("%d proposed rows, want 1", n)
	}
}

func proposedCount(t *testing.T, h *harness) int {
	t.Helper()
	var n int
	if err := h.d.SQL.Get(&n, `SELECT COUNT(*) FROM virtual_fs_entries WHERE status = ?`, db.StatusProposed); err != nil {
		t.Fatal(err)
	}
	return n
}

// A screenshot joins a placed Screenshots folder, and nothing else does.
func TestRouteSpecialFolders(t *testing.T) {
	h := newHarness(t)
	h.placeFile(t, metaWith("2024:03:02 10:00:00", 0, 0, 3024, 4032), "2024/03_March/Screenshots/IMG_0001.PNG",
		[]string{LevelYear, LevelMonth, LevelScreenshots},
		[]Bounds{{{Year: []int{2024}}}, {{Month: []int{3}}}, {{}}})
	shot := h.addScreenshot(t, "dump/SHOT.PNG", metaWith("2024:03:04 10:00:00", 0, 0, 1170, 2532))
	photo := h.addFile(t, "dump/NEW.HEIC", classifier.MediaTypeImage, metaWith("2024:03:04 10:00:00", 0, 0, 3024, 4032))

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate}
	got := h.build(t, cfg, nil)
	if got[shot].TargetPath != "2024/03_March/Screenshots/SHOT.PNG" {
		t.Errorf("screenshot = %q", got[shot].TargetPath)
	}
	if got[photo].TargetPath != "2024/03_March/04/NEW.HEIC" {
		t.Errorf("photo = %q, want its own day, not Screenshots", got[photo].TargetPath)
	}
}
