// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/classifier"

	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// runPlan builds the target path for each master under cfg, with no
// database and no geonames — Plan is a pure function of masters/labels/cfg.
func runPlan(t *testing.T, masters []masterFile, cfg Config) []masterFile {
	t.Helper()
	if err := Plan(context.Background(), masters, cfg, nil, logger.NewNoopLogger()); err != nil {
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

	// a day that is nothing but everyday shots: the city folder would repeat
	// one name and say nothing, so it stays suppressed
	masters := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "home1.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Indore", atSavedPlace: true},
		{FileDir: "/src", FileName: "home2.jpg", DBDateTaken: new("2024:08:02 11:00:00"), location: "Indore", atSavedPlace: true},
	}, cfg)
	for _, m := range masters {
		if want := "2024/08_August/02/" + m.FileName; m.targetPath != want {
			t.Errorf("saved-place file: got %q, want %q (location folder should be suppressed)", m.targetPath, want)
		}
	}
}

// TestPlanSavedPlacesDateOnlyLiftsOnAMixedDay is the reported bug: the day held
// a loose pile of home-town photos *and* a nested folder for everything else,
// so it read as half-sorted. A day either nests its locations or it doesn't.
func TestPlanSavedPlacesDateOnlyLiftsOnAMixedDay(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.SavedPlacesDateOnly = true
	cfg.CollapseLevels = false

	masters := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "home.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Indore", atSavedPlace: true},
		{FileDir: "/src", FileName: "trip.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Goa", atSavedPlace: false},
	}, cfg)

	if want := "2024/08_August/02/Indore/home.jpg"; masters[0].targetPath != want {
		t.Errorf("saved-place file: got %q, want %q (its city comes back beside the other folder)", masters[0].targetPath, want)
	}
	if want := "2024/08_August/02/Goa/trip.jpg"; masters[1].targetPath != want {
		t.Errorf("trip file: got %q, want %q (location folder should render)", masters[1].targetPath, want)
	}
}

// TestPlanMergeKeepsOneFolderPerDay is the reported bug in its plainest form:
// `01_02` sat next to a `02`, so the 2nd of the month was in two date folders
// at once. A day belongs to exactly one, so a location's run has to break
// where the rest of that day disagrees with it.
func TestPlanMergeKeepsOneFolderPerDay(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.CollapseLevels = false
	cfg.ClusterGap = time.Minute // keep the days in their own clusters

	got := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "home01.jpg", DBDateTaken: new("2024:08:01 10:00:00"), location: "Indore", atSavedPlace: true},
		{FileDir: "/src", FileName: "home02.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Indore", atSavedPlace: true},
		{FileDir: "/src", FileName: "other02.jpg", DBDateTaken: new("2024:08:02 20:00:00"), location: "Goa"},
	}, cfg)
	want := []string{
		// day 01 is home-only, so no city folder and — with day 02 out of the
		// run — no range either
		"2024/08_August/01/home01.jpg",
		"2024/08_August/02/Indore/home02.jpg",
		"2024/08_August/02/Goa/other02.jpg",
	}
	for i, m := range got {
		if m.targetPath != want[i] {
			t.Errorf("%s: got %q, want %q", m.FileName, m.targetPath, want[i])
		}
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

// TestPlanClusterStartAnchorsYearAndMonth pins the calendar fix: one event
// that runs over midnight on New Year's Eve is one folder, not two Year trees.
func TestPlanClusterStartAnchorsYearAndMonth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.CollapseLevels = false
	cfg.MergeSameLocationDays = false

	// 6h apart, so all three sit in one cluster starting 2023-12-31
	got := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "a.jpg", DBDateTaken: new("2023:12:31 20:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "b.jpg", DBDateTaken: new("2024:01:01 02:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "c.jpg", DBDateTaken: new("2024:01:01 08:00:00"), location: "Goa"},
	}, cfg)
	want := []string{
		"2023/12_December/31/Goa/a.jpg",
		// the day is the file's own, but qualified with its own month: a bare
		// "01" under 12_December reads as (and collides with) Dec 01
		"2023/12_December/Jan_01/Goa/b.jpg",
		"2023/12_December/Jan_01/Goa/c.jpg",
	}
	for i, m := range got {
		if m.targetPath != want[i] {
			t.Errorf("%s: got %q, want %q", m.FileName, m.targetPath, want[i])
		}
	}
}

// TestPlanBoundaryDayDoesNotCollideWithRealDay is the reported bug: Jan 01
// files pulled into December by their cluster landed in "12_December/01",
// which is where the library's real Dec 01 files already live.
func TestPlanBoundaryDayDoesNotCollideWithRealDay(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.CollapseLevels = false
	cfg.MergeSameLocationDays = true

	got := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "dec01.jpg", DBDateTaken: new("2023:12:01 10:00:00"), location: "Banjar"},
		{FileDir: "/src", FileName: "nye.jpg", DBDateTaken: new("2023:12:31 20:00:00"), location: "Banjar"},
		{FileDir: "/src", FileName: "jan01.jpg", DBDateTaken: new("2024:01:01 02:00:00"), location: "Banjar"},
	}, cfg)
	want := []string{
		"2023/12_December/01/Banjar/dec01.jpg",
		"2023/12_December/31/Banjar/nye.jpg",
		"2023/12_December/Jan_01/Banjar/jan01.jpg",
	}
	for i, m := range got {
		if m.targetPath != want[i] {
			t.Errorf("%s: got %q, want %q", m.FileName, m.targetPath, want[i])
		}
	}
}

// TestPlanScreenshotFollowsClusterMonth covers the short-circuit path: a
// screenshot skips Rules entirely, so it has to pick up the cluster's month
// before that happens or it lands in a different Year to its own event.
func TestPlanScreenshotFollowsClusterMonth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}

	got := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "a.jpg", DBDateTaken: new("2023:12:31 20:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "shot.png", DBDateTaken: new("2024:01:01 02:00:00"), IsScreenshot: true},
	}, cfg)
	if want := "2023/12_December/Screenshots/shot.png"; got[1].targetPath != want {
		t.Errorf("screenshot: got %q, want %q", got[1].targetPath, want)
	}
}

func TestPlanUnknownLocationOnlyBesideLocatedSiblings(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.CollapseLevels = false
	cfg.MergeSameLocationDays = false
	// keep the GPS-less shots in their own cluster, so clusterAndSpill can't
	// hand them Goa before the location level is decided
	cfg.ClusterGap = time.Minute

	got := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "gps.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "nogps.jpg", DBDateTaken: new("2024:08:02 20:00:00")},
		{FileDir: "/src", FileName: "alone.jpg", DBDateTaken: new("2024:08:03 10:00:00")},
	}, cfg)
	want := []string{
		"2024/08_August/02/Goa/gps.jpg",
		"2024/08_August/02/Unknown/nogps.jpg",
		"2024/08_August/03/alone.jpg", // whole day unlocated — no Unknown folder
	}
	for i, m := range got {
		if m.targetPath != want[i] {
			t.Errorf("%s: got %q, want %q", m.FileName, m.targetPath, want[i])
		}
	}
}

func TestPlanUnknownLocationMergesDaysLikeAnyOther(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.CollapseLevels = false
	cfg.MergeSameLocationDays = true
	cfg.ClusterGap = time.Minute

	got := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "gps02.jpg", DBDateTaken: new("2024:08:02 10:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "nogps02.jpg", DBDateTaken: new("2024:08:02 20:00:00")},
		{FileDir: "/src", FileName: "gps03.jpg", DBDateTaken: new("2024:08:03 10:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "nogps03.jpg", DBDateTaken: new("2024:08:03 20:00:00")},
	}, cfg)
	want := []string{
		"2024/08_August/02_03/Goa/gps02.jpg",
		"2024/08_August/02_03/Unknown/nogps02.jpg",
		"2024/08_August/02_03/Goa/gps03.jpg",
		"2024/08_August/02_03/Unknown/nogps03.jpg",
	}
	for i, m := range got {
		if m.targetPath != want[i] {
			t.Errorf("%s: got %q, want %q", m.FileName, m.targetPath, want[i])
		}
	}
}

// TestPlanSidecarFollowsScreenshot is the reported bug: IMG_0231.AAE landed in
// a date folder while IMG_0231.PNG sat in Screenshots. The edit variant
// IMG_E0231.JPG folds to the same capture stem but carries a DateTimeOriginal
// 13s later, which the old exact-second agreement read as a conflict and threw
// the whole group away.
func TestPlanSidecarFollowsScreenshot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}

	got := runPlan(t, []masterFile{
		{FileDir: "/phone", FileName: "IMG_0231.AAE", MediaType: classifier.MediaTypeSidecar, ModifiedAt: "2025:12:02 23:00:02"},
		{FileDir: "/phone", FileName: "IMG_0231.PNG", DBDateTaken: new("2025:12:02 23:00:02"), IsScreenshot: true},
		{FileDir: "/phone", FileName: "IMG_E0231.JPG", DBDateTaken: new("2025:12:02 23:00:15"), IsScreenshot: true},
	}, cfg)
	for _, m := range got {
		if want := "2025/12_December/Screenshots/" + m.FileName; m.targetPath != want {
			t.Errorf("%s: got %q, want %q", m.FileName, m.targetPath, want)
		}
	}
}

// TestPlanLongClusterKeepsOwnMonth is the other half of the New Year rule: a
// cluster grows for as long as shots keep landing inside the gap, so a holiday
// shot every few hours is one unbroken cluster running for days. Filing its
// January photos under the previous December — in the wrong Year tree, as
// "Jan_05" — was a reported bug. Past maxFolderSpan every file keeps its own
// month.
func TestPlanLongClusterKeepsOwnMonth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.CollapseLevels = false
	cfg.MergeSameLocationDays = false

	// 10h apart throughout: one cluster (gap is 12h), spanning 40h
	got := runPlan(t, []masterFile{
		{FileDir: "/src", FileName: "a.jpg", DBDateTaken: new("2025:12:31 10:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "b.jpg", DBDateTaken: new("2025:12:31 20:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "c.jpg", DBDateTaken: new("2026:01:01 06:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "d.jpg", DBDateTaken: new("2026:01:01 16:00:00"), location: "Goa"},
		{FileDir: "/src", FileName: "e.jpg", DBDateTaken: new("2026:01:02 02:00:00"), location: "Goa"},
	}, cfg)
	want := []string{
		"2025/12_December/31/Goa/a.jpg",
		"2025/12_December/31/Goa/b.jpg",
		"2026/01_January/01/Goa/c.jpg",
		"2026/01_January/01/Goa/d.jpg",
		"2026/01_January/02/Goa/e.jpg",
	}
	for i, m := range got {
		if m.targetPath != want[i] {
			t.Errorf("%s: got %q, want %q", m.FileName, m.targetPath, want[i])
		}
	}
}

// TestPlanCaptureGroupMemberTakesLeaderFolderTime pins what persist writes as
// taken_at against the directory the same file was given. A sidecar has no
// EXIF time and falls back to its file mtime, which can sit months from the
// capture it belongs to; it still follows the group's directory, so without the
// leader's folder time it surfaced in a review time slice showing one lone
// folder out of another year (a reported bug).
func TestPlanCaptureGroupMemberTakesLeaderFolderTime(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.CollapseLevels = false

	got := runPlan(t, []masterFile{
		{FileDir: "/phone", FileName: "IMG_0044.HEIC", DBDateTaken: new("2025:11:30 09:00:00"), location: "Banjar"},
		// copied off the phone months later, so the mtime says March
		{FileDir: "/phone", FileName: "IMG_0044.AAE", MediaType: classifier.MediaTypeSidecar, ModifiedAt: "2026:03:01 12:00:00"},
	}, cfg)

	var leader, sidecar *masterFile
	for i := range got {
		if got[i].MediaType == classifier.MediaTypeSidecar {
			sidecar = &got[i]
		} else {
			leader = &got[i]
		}
	}
	if want := "2025/11_November/30/Banjar/IMG_0044.AAE"; sidecar.targetPath != want {
		t.Fatalf("sidecar path: got %q, want %q", sidecar.targetPath, want)
	}
	if got, want := sidecar.folderTime(), leader.folderTime(); !got.Equal(want) {
		t.Errorf("sidecar folder time: got %s, want %s (the row's taken_at has to match its own path)", got, want)
	}
}

// TestPlanCaptureGroupRejectsReusedCounter keeps the guard the agreement window
// exists for: the same filename reused by a later shoot must not be forced into
// one directory.
func TestPlanCaptureGroupRejectsReusedCounter(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.MergeSameLocationDays = false

	got := runPlan(t, []masterFile{
		{FileDir: "/cam", FileName: "IMG_0231.JPG", DBDateTaken: new("2024:03:01 09:00:00"), location: "Goa"},
		{FileDir: "/cam", FileName: "IMG_0231.CR2", DBDateTaken: new("2024:03:04 09:00:00"), location: "Pune"},
	}, cfg)
	want := []string{"2024/03_March/01/Goa/IMG_0231.JPG", "2024/03_March/04/Pune/IMG_0231.CR2"}
	for i, m := range got {
		if m.targetPath != want[i] {
			t.Errorf("%s: got %q, want %q", m.FileName, m.targetPath, want[i])
		}
	}
}

// TestPlanCaptureGroupRejectsDeviceMismatch is the reported bug: two different
// phones reused the same filename counter on the same day (well within the
// agreement window, e.g. after a phone upgrade), and the window check alone
// wasn't enough to keep them apart — DeviceA's photo/edit and the reused
// counter's stray sidecar must not be forced into one directory.
func TestPlanCaptureGroupRejectsDeviceMismatch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	cfg.MergeSameLocationDays = false

	got := runPlan(t, []masterFile{
		{
			FileDir: "/phone", FileName: "IMG_1051.HEIC",
			DBDateTaken: new("2025:12:14 16:55:59"), DBMake: new("Apple"), DBModel: new("iPhone 17 Pro Max"),
			location: "Indore",
		},
		{
			FileDir: "/phone", FileName: "IMG_1051.JPG",
			DBDateTaken: new("2025:12:14 16:56:10"), DBMake: new("Apple"), DBModel: new("iPhone 16 Pro"),
		},
	}, cfg)
	want := []string{
		"2025/12_December/14/Indore/IMG_1051.HEIC",
		"2025/12_December/14/Unknown/IMG_1051.JPG",
	}
	for i, m := range got {
		if m.targetPath != want[i] {
			t.Errorf("%s: got %q, want %q", m.FileName, m.targetPath, want[i])
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

// TestSortByCaptureTimePermutation pins both halves of the sort: the order, and
// that the permutation is a bijection. An earlier in-place version used the
// swap form, which implements result[p[i]]=src[i] rather than the
// result[i]=src[p[i]] this needs — it duplicated masters and dropped others
// while still returning a plausible-looking slice.
func TestSortByCaptureTimePermutation(t *testing.T) {
	// deliberately scrambled, with a tie (b/c) to pin stability and a zero
	// takenAt (undated) to pin that it sorts first without overflowing
	at := func(s string) time.Time {
		ts, err := time.Parse("2006-01-02 15:04:05", s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	masters := []masterFile{
		{FileName: "e", takenAt: at("2024-08-05 10:00:00")},
		{FileName: "b", takenAt: at("2024-08-02 10:00:00")},
		{FileName: "d", takenAt: at("2024-08-04 10:00:00")},
		{FileName: "undated"},
		{FileName: "c", takenAt: at("2024-08-02 10:00:00")}, // ties with b, must stay after it
		{FileName: "a", takenAt: at("2024-08-01 10:00:00")},
	}

	sortByCaptureTime(masters)

	var got []string
	for i := range masters {
		got = append(got, masters[i].FileName)
	}
	want := []string{"undated", "a", "b", "c", "d", "e"}
	if !slices.Equal(got, want) {
		t.Errorf("order: got %v, want %v", got, want)
	}

	// a permutation loses and duplicates nothing
	seen := map[string]int{}
	for _, n := range got {
		seen[n]++
	}
	for n, c := range seen {
		if c != 1 {
			t.Errorf("%q appears %d times, want exactly 1", n, c)
		}
	}
}

// fanoutFixture is a library shaped to hit every branch the parallel passes
// touch: target-path collisions, captureDirs groups, screenshots, undated
// files, GPS-less files that need clustering, and enough volume to span
// several of forEachMaster's 512-index runs.
func fanoutFixture() []masterFile {
	var masters []masterFile
	add := func(m masterFile) {
		m.FileID = int64(len(masters) + 1)
		masters = append(masters, m)
	}
	for i := range 2000 {
		day := i%28 + 1
		dto := fmt.Sprintf("2024:%02d:%02d %02d:%02d:00", i%12+1, day, i%24, i%60)
		m := masterFile{
			FileDir:     fmt.Sprintf("/src/trip%d", i%13),
			FileName:    fmt.Sprintf("IMG_%04d.HEIC", i%700), // %700 forces real collisions
			MediaType:   classifier.MediaTypeImage,
			DBDateTaken: &dto,
			DBWidth:     new(int64(3024)),
			DBHeight:    new(int64(4032)),
			DBMake:      new("Apple"),
			DBModel:     new("iPhone 15 Pro"),
		}
		switch i % 11 {
		case 0:
			m.location = []string{"Goa", "Indore", "Pune"}[i%3]
		case 1:
			m.IsScreenshot = true
		case 2:
			m.DBDateTaken = nil // undated: falls to the Unsorted shard
		case 3:
			m.MediaType = classifier.MediaTypeVideo
		}
		add(m)
	}
	// same name, same everything derivable, different source folders: these all
	// land on one directory, so the sequential taken loop has to hand out _2.._6
	// by arrival order. The case-only variant covers its ToLower key.
	dup := "2024:07:04 12:00:00"
	for i, dir := range []string{"/a", "/b", "/c", "/d", "/e", "/f"} {
		name := "DUP.HEIC"
		if i == 3 {
			name = "dup.heic"
		}
		add(masterFile{
			FileDir: dir, FileName: name, MediaType: classifier.MediaTypeImage,
			DBDateTaken: &dup, location: "Goa",
			DBWidth: new(int64(3024)), DBHeight: new(int64(4032)),
			DBMake: new("Apple"), DBModel: new("iPhone 15 Pro"),
		})
	}

	// a captureDirs group: same dir + captureStem, agreeing EXIF time, so
	// every member takes the leader's directory and skips dirFor entirely
	grp := "2024:06:01 09:30:00"
	for _, n := range []string{"IMG_9001.HEIC", "IMG_E9001.JPG", "IMG_O9001.JPG"} {
		add(masterFile{
			FileDir: "/src/group", FileName: n, MediaType: classifier.MediaTypeImage,
			DBDateTaken: &grp, location: "Goa",
			DBWidth: new(int64(4032)), DBHeight: new(int64(3024)),
		})
	}
	return masters
}

// derived is everything Plan writes onto a master, so a mismatch anywhere in
// the build surfaces rather than only a differing target path.
func derived(m *masterFile) string {
	return fmt.Sprintf("%d|%s|%s|%s|%s|%s|%v",
		m.FileID, m.targetPath, m.locationDir,
		m.clusterID, m.location, m.eventSegment, m.atSavedPlace)
}

// TestPlanFanoutMatchesSequential pins the invariant the whole package now
// leans on: deriveAll, applyNameCase and buildTargets' directory pass run on
// cfg.Workers goroutines, and none of that may change a single byte of the
// proposal. Also runs the parallel build twice, so a nondeterministic pass
// (map iteration leaking into a name) fails rather than flaking in the field.
func TestPlanFanoutMatchesSequential(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation, RuleDevice, RuleOrientation, RuleMedia}

	run := func(workers int) []string {
		c := cfg
		c.Workers = workers
		masters := fanoutFixture()
		if err := Plan(context.Background(), masters, c, nil, logger.NewNoopLogger()); err != nil {
			t.Fatalf("Plan(workers=%d): %v", workers, err)
		}
		out := make([]string, len(masters))
		for i := range masters {
			out[i] = derived(&masters[i])
		}
		return out
	}

	seq := run(1)
	for _, workers := range []int{2, 8, 8, 32} {
		got := run(workers)
		if len(got) != len(seq) {
			t.Fatalf("workers=%d: %d masters, want %d", workers, len(got), len(seq))
		}
		for i := range seq {
			if got[i] != seq[i] {
				t.Fatalf("workers=%d, position %d:\n parallel: %s\n sequential: %s", workers, i, got[i], seq[i])
			}
		}
	}

	// the fixture must actually reach the branches this is meant to cover
	var collisions int
	for _, s := range seq {
		if strings.Contains(s, "_2.HEIC") {
			collisions++
		}
	}
	if collisions == 0 {
		t.Error("fixture produced no collision suffixes — the sequential taken loop is untested")
	}
}

// TestPlanDegenerateInputs covers the shapes the fan-out has no files to chew
// on, plus cancellation: forEachMaster still starts its workers for an empty
// slice, and buildTargets' directory pass can bail mid-way leaving holes in
// dirs, which Plan must report rather than hand on to persist.
func TestPlanDegenerateInputs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation, RuleDevice}
	log := logger.NewNoopLogger()

	for _, workers := range []int{0, 1, 8} {
		c := cfg
		c.Workers = workers
		for _, n := range []int{0, 1, 2} {
			if err := Plan(context.Background(), fanoutFixture()[:n], c, nil, log); err != nil {
				t.Fatalf("workers=%d n=%d: %v", workers, n, err)
			}
		}
		sortByCaptureTime(nil)
		sortByCaptureTime([]masterFile{{}})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := Plan(ctx, fanoutFixture(), c, nil, log); err == nil {
			t.Errorf("workers=%d: cancelled Plan returned nil, so a partial proposal would reach persist", workers)
		}
	}
}
