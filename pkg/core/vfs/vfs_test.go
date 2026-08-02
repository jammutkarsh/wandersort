// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

type entryRow struct {
	FileID           int64   `db:"file_id"`
	TargetPath       string  `db:"target_path"`
	ClusterID        *string `db:"cluster_id"`
	Status           string  `db:"status"`
	Suggestion       *string `db:"suggestion"`
	SuggestionSource *string `db:"suggestion_source"`
}

type harness struct {
	d      *db.DB
	nextID int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{d: dbtest.New(t)}
}

// addFile seeds a registry row (under the /src root) plus a master metadata
// row carrying the given EXIF values, as the hash phase would have persisted
func (h *harness) addFile(t *testing.T, relPath, mediaType string, meta classifier.CommonMetadata) int64 {
	t.Helper()
	h.nextID++
	id := h.nextID
	dir := filepath.Join("/src", filepath.Dir(relPath))
	name := filepath.Base(relPath)
	if _, err := h.d.ExecContext(context.Background(), `
		INSERT INTO file_registry (id, file_dir, file_name, file_size, file_modified_at,
			file_extension, media_type, discovered_at, last_seen_at)
		VALUES (?, ?, ?, 1024, '2024-06-01T10:00:00.000000000Z', ?, ?,
			'2024-06-01T10:00:00.000000000Z', '2024-06-01T10:00:00.000000000Z')`,
		id, dir, name, filepath.Ext(name), mediaType); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.ExecContext(context.Background(), `
		INSERT INTO file_metadata (file_hash, file_id,
			exif_image_width, exif_image_height, exif_orientation,
			exif_gps_latitude, exif_gps_longitude, exif_make, exif_model,
			exif_date_time_original, exif_create_date, exif_creation_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fmt.Sprintf("hash-%d", id), id,
		db.IntOrNil(meta.ImageWidth), db.IntOrNil(meta.ImageHeight), db.IntOrNil(meta.Orientation),
		db.FloatOrNil(meta.GPSLatitude), db.FloatOrNil(meta.GPSLongitude),
		db.StrOrNil(meta.Make), db.StrOrNil(meta.Model),
		db.StrOrNil(meta.DateTimeOriginal), db.StrOrNil(meta.CreateDate), db.StrOrNil(meta.CreationDate)); err != nil {
		t.Fatal(err)
	}
	return id
}

// addScreenshot seeds a file the same way addFile does, then flips the
// is_screenshot marker exif would have set from Description/UserComment.
func (h *harness) addScreenshot(t *testing.T, relPath string, meta classifier.CommonMetadata) int64 {
	t.Helper()
	id := h.addFile(t, relPath, classifier.MediaTypeImage, meta)
	if _, err := h.d.ExecContext(context.Background(),
		`UPDATE file_metadata SET is_screenshot = 1 WHERE file_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *harness) build(t *testing.T, cfg Config, geo *location.Resolver) map[int64]entryRow {
	t.Helper()
	vfs := &VFS{
		db:       h.d,
		log:      logger.NewNoopLogger(),
		cfg:      cfg,
		resolver: geo,
	}
	if _, err := vfs.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.d.Writer.Flush()

	var rows []entryRow
	if err := h.d.SQL.Select(&rows, `
		SELECT file_id, target_path, cluster_id, status, suggestion, suggestion_source
		FROM virtual_fs_entries`); err != nil {
		t.Fatal(err)
	}
	byID := map[int64]entryRow{}
	for _, r := range rows {
		byID[r.FileID] = r
	}
	return byID
}

func metaWith(dto string, lat, lon float64, w, h int) classifier.CommonMetadata {
	m := classifier.CommonMetadata{DateTimeOriginal: dto, Make: "Apple", Model: "iPhone 15 Pro"}
	if lat != 0 {
		m.GPSLatitude = fmt.Sprintf("%f", lat)
		m.GPSLongitude = fmt.Sprintf("%f", lon)
	}
	if w != 0 {
		m.ImageWidth = fmt.Sprintf("%d", w)
		m.ImageHeight = fmt.Sprintf("%d", h)
	}
	return m
}

func TestBuildFullExif(t *testing.T) {
	h := newHarness(t)
	// 15.5439,73.7553 is Calangute, Goa — a real geonames entry with no
	// disambiguation qualifier needed (unique name)
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5439, 73.7553, 3024, 4032))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation, RuleOrientation, RuleMedia}
	rows := h.build(t, cfg, geo)

	// device/orientation/media all have a single value across this one-file
	// library, so every one of those levels collapses away
	want := "2024/06_June/Calangute/IMG_0001.HEIC"
	if rows[id].TargetPath != want {
		t.Errorf("target = %q, want %q", rows[id].TargetPath, want)
	}
	if rows[id].Status != db.StatusProposed {
		t.Errorf("status = %q, want PROPOSED", rows[id].Status)
	}
	if rows[id].ClusterID != nil {
		t.Errorf("directly located file should have NULL cluster_id, got %v", *rows[id].ClusterID)
	}
}

// TestScreenshotGroupsByMonthOnly checks screenshots skip the configured
// Rules entirely (location/device/orientation) and land together under
// <Year>/<Month>/Screenshots, regardless of GPS or capture day.
func TestScreenshotGroupsByMonthOnly(t *testing.T) {
	h := newHarness(t)
	a := h.addScreenshot(t, "a.PNG", metaWith("2024:06:03 10:00:00", 15.5439, 73.7553, 1170, 2532))
	b := h.addScreenshot(t, "b.jpg", metaWith("2024:06:20 09:00:00", 0, 0, 1170, 2532))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation, RuleDevice, RuleOrientation, RuleMedia}
	rows := h.build(t, cfg, geo)

	for _, id := range []int64{a, b} {
		want := "2024/06_June/Screenshots/" + filepath.Base(rows[id].TargetPath)
		if rows[id].TargetPath != want {
			t.Errorf("file %d target = %q, want %q", id, rows[id].TargetPath, want)
		}
	}
}

func TestClusterSpillover(t *testing.T) {
	h := newHarness(t)
	// two files without GPS, one with — all within the 12h gap
	a := h.addFile(t, "dump/DSC_0001.JPG", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))
	b := h.addFile(t, "dump/DSC_0002.JPG", "IMAGE", metaWith("2024:06:03 12:00:00", 0, 0, 4000, 3000))
	// 32.2432,77.1892 is the real Manali, Himachal Pradesh — the geonames
	// row for it carries a diacritic ("Manāli") that's stripped for display,
	// and it's the only candidate within the tight first-pass search box, so
	// it resolves to the bare, unqualified "Manali"
	c := h.addFile(t, "dump/IMG_0003.HEIC", "IMAGE", metaWith("2024:06:03 11:00:00", 32.2432, 77.1892, 3024, 4032))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation, RuleOrientation, RuleMedia}
	rows := h.build(t, cfg, geo)

	for _, id := range []int64{a, b} {
		if got := rows[id].TargetPath; got != "2024/06_June/Manali/Horizontal/Photos/"+filepath.Base(rows[id].TargetPath) {
			// location segment is what matters
			if want := "2024/06_June/Manali/"; len(got) < len(want) || got[:len(want)] != want {
				t.Errorf("file %d target = %q, want prefix %q", id, got, want)
			}
		}
		if rows[id].SuggestionSource == nil || *rows[id].SuggestionSource != SuggestionSpillover {
			t.Errorf("file %d suggestion_source = %v, want SPILLOVER", id, rows[id].SuggestionSource)
		}
		if rows[id].ClusterID == nil {
			t.Errorf("file %d should carry a cluster_id", id)
		}
	}
	if rows[c].ClusterID != nil {
		t.Errorf("directly located file should have NULL cluster_id")
	}
}

func TestUnresolvedEventSegmentAndGapSplit(t *testing.T) {
	h := newHarness(t)
	// no GPS anywhere; >12h gap splits into two clusters
	a := h.addFile(t, "dump/DSC_0001.JPG", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))
	b := h.addFile(t, "dump/DSC_0002.JPG", "IMAGE", metaWith("2024:06:05 09:00:00", 0, 0, 4000, 3000))

	rows := h.build(t, DefaultConfig(), nil)

	wantA := "2024/06_June/03/DSC_0001.JPG"
	wantB := "2024/06_June/05/DSC_0002.JPG"
	if rows[a].TargetPath != wantA {
		t.Errorf("a = %q, want %q", rows[a].TargetPath, wantA)
	}
	if rows[b].TargetPath != wantB {
		t.Errorf("b = %q, want %q", rows[b].TargetPath, wantB)
	}
	if *rows[a].ClusterID == *rows[b].ClusterID {
		t.Errorf("gap-split files should be in different clusters")
	}
}

func TestUserLabelSuggestion(t *testing.T) {
	h := newHarness(t)
	h.addFile(t, "dump/DSC_0001.JPG", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))
	// label casing is user-chosen and must survive title-casing
	if _, err := h.d.ExecContext(context.Background(), `
		INSERT INTO user_labels (label, kind, time_start, time_end)
		VALUES ('Manali TRIP', 'EVENT', '2024-06-02T00:00:00Z', '2024-06-06T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	rows := h.build(t, DefaultConfig(), nil)
	for _, r := range rows {
		if r.Suggestion == nil || *r.Suggestion != "Manali TRIP" {
			t.Errorf("suggestion = %v, want 'Manali TRIP'", r.Suggestion)
		}
		if r.SuggestionSource == nil || *r.SuggestionSource != SuggestionUserLabel {
			t.Errorf("suggestion_source = %v, want USER_LABEL", r.SuggestionSource)
		}
	}
}

// TestAnchorSuggestion covers a fully unresolved cluster (no GPS at all)
// falling back to a *confirmed* saved-place anchor for its suggestion.
// anchorCities deliberately no longer falls back to "the library's most
// frequent city" with no confirmed anchor — see the anchorCities doc comment
// for why (a real reported bug: an unrelated DSLR photo with no GPS anywhere
// nearby was "suggested" whatever city happened to dominate the library).
func TestAnchorSuggestion(t *testing.T) {
	h := newHarness(t)
	u := h.addFile(t, "dump/DSC_0009.JPG", "IMAGE", metaWith("2024:06:20 10:00:00", 0, 0, 4000, 3000))

	cfg := DefaultConfig()
	addHomeAnchor(&cfg, "Indore", 0, 0)
	rows := h.build(t, cfg, nil)
	if rows[u].Suggestion == nil || *rows[u].Suggestion != "Indore" {
		t.Errorf("suggestion = %v, want 'Indore'", rows[u].Suggestion)
	}
	if rows[u].SuggestionSource == nil || *rows[u].SuggestionSource != SuggestionAnchor {
		t.Errorf("suggestion_source = %v, want ANCHOR", rows[u].SuggestionSource)
	}
}

// TestNoAnchorFallsThroughToSourceFolder covers the fixed bug directly: with
// no confirmed anchor and no matching EVENT label, a fully unresolved cluster
// must NOT invent a location from library-wide frequency — it should fall
// through to the next rung (source folder name) instead.
func TestNoAnchorFallsThroughToSourceFolder(t *testing.T) {
	h := newHarness(t)
	// plenty of directly-resolved "Banjar" photos elsewhere in the library —
	// none of this should leak into the unrelated DSLR cluster below
	// 31.63722,77.34028 is the real Banjar, Himachal Pradesh
	for i := range 5 {
		h.addFile(t, fmt.Sprintf("phone/IMG_%d.HEIC", i), "IMAGE", metaWith("2024:05:01 10:00:00", 31.63722, 77.34028, 3024, 4032))
	}
	geo := installtest.Resolver(t)

	u := h.addFile(t, "Diwali 2024/IMG_9001.JPG", "IMAGE", metaWith("2024:08:15 10:00:00", 0, 0, 6000, 4000))

	rows := h.build(t, DefaultConfig(), geo)
	if rows[u].Suggestion == nil || *rows[u].Suggestion != "Diwali 2024" {
		t.Errorf("suggestion = %v, want the source folder 'Diwali 2024', not the library's frequent city", rows[u].Suggestion)
	}
	if rows[u].SuggestionSource == nil || *rows[u].SuggestionSource != SuggestionSourceFolder {
		t.Errorf("suggestion_source = %v, want SOURCE_FOLDER", rows[u].SuggestionSource)
	}
}

func TestSourceFolderSuggestion(t *testing.T) {
	h := newHarness(t)
	u := h.addFile(t, "Goa Trip 2024/DSC_0001.JPG", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))

	rows := h.build(t, DefaultConfig(), nil)
	if rows[u].Suggestion == nil || *rows[u].Suggestion != "Goa Trip 2024" {
		t.Errorf("suggestion = %v, want 'Goa Trip 2024'", rows[u].Suggestion)
	}
	if rows[u].SuggestionSource == nil || *rows[u].SuggestionSource != SuggestionSourceFolder {
		t.Errorf("suggestion_source = %v, want SOURCE_FOLDER", rows[u].SuggestionSource)
	}
}

func TestCoalescingSameCity(t *testing.T) {
	h := newHarness(t)
	// two clusters days apart, both resolve to the same city → same folder
	a := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 22.71667, 75.85, 3024, 4032))
	b := h.addFile(t, "d/IMG_0002.HEIC", "IMAGE", metaWith("2024:06:20 10:00:00", 22.71667, 75.85, 3024, 4032))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation, RuleOrientation, RuleMedia}
	rows := h.build(t, cfg, geo)
	dirA := filepath.Dir(rows[a].TargetPath)
	dirB := filepath.Dir(rows[b].TargetPath)
	if dirA != dirB {
		t.Errorf("same-city clusters should coalesce: %q vs %q", dirA, dirB)
	}
}

func TestCollisionSuffix(t *testing.T) {
	h := newHarness(t)
	a := h.addFile(t, "d1/IMG_3162.JPG", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))
	b := h.addFile(t, "d2/IMG_3162.JPG", "IMAGE", metaWith("2024:06:03 11:00:00", 0, 0, 4000, 3000))

	rows := h.build(t, DefaultConfig(), nil)
	names := map[string]bool{
		filepath.Base(rows[a].TargetPath): true,
		filepath.Base(rows[b].TargetPath): true,
	}
	if !names["IMG_3162.JPG"] || !names["IMG_3162_2.JPG"] {
		t.Errorf("want IMG_3162.JPG + IMG_3162_2.JPG, got %v", names)
	}
}

// TestSameStemDifferentEventsLandSeparately covers a real reported bug: an
// earlier version force-grouped files sharing a filename stem (assuming
// same stem = same Live Photo/RAW+JPG capture), but phone/camera filename
// counters get reused across entirely unrelated shoots — especially old
// iPhone photos. Two files with the same stem, same source dir, but
// unrelated capture times must NOT be forced into one directory anymore;
// each file's own derived time decides its directory independently.
func TestSameStemDifferentEventsLandSeparately(t *testing.T) {
	h := newHarness(t)
	a := h.addFile(t, "d/IMG_0042.HEIC", "IMAGE", metaWith("2019:03:01 10:00:00", 0, 0, 3024, 4032))
	b := h.addFile(t, "d/IMG_0042.MOV", "VIDEO", metaWith("2021:11:15 18:00:00", 0, 0, 1920, 1080))

	rows := h.build(t, DefaultConfig(), nil)
	dirA := filepath.Dir(rows[a].TargetPath)
	dirB := filepath.Dir(rows[b].TargetPath)
	if dirA == dirB {
		t.Errorf("same-stem files from unrelated shoots landed in the same dir %q — capture-group forcing should be gone", dirA)
	}
}

// TestSameStemSameCaptureStillCoLocatesByOwnAttributes covers the case that
// used to need forced grouping (a real Live Photo pair, same moment, same
// GPS): with Rules excluding media type, both members still land in the
// same directory purely because their own derived location/date agree —
// no stem-based special-casing required to get the intuitive result.
func TestSameStemSameCaptureStillCoLocatesByOwnAttributes(t *testing.T) {
	h := newHarness(t)
	a := h.addFile(t, "d/IMG_0042.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5439, 73.7553, 3024, 4032))
	b := h.addFile(t, "d/IMG_0042.MOV", "VIDEO", metaWith("2024:06:03 10:00:00", 15.5439, 73.7553, 1920, 1080))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation} // no media split, so a real Live Photo pair still co-locates
	rows := h.build(t, cfg, geo)

	dirA := filepath.Dir(rows[a].TargetPath)
	dirB := filepath.Dir(rows[b].TargetPath)
	if dirA != dirB {
		t.Errorf("same-moment, same-GPS pair split across %q and %q", dirA, dirB)
	}
}

// TestSidecarWithNoOwnTimestampFallsBackToModTime covers .aae-style sidecars:
// with no independent EXIF timestamp, deriveAll's takenAt falls back to file
// mtime like any other file — no stem-matching to a "paired" photo needed.
func TestSidecarWithNoOwnTimestampFallsBackToModTime(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "d/IMG_0042.AAE", "SIDECAR", classifier.CommonMetadata{})

	rows := h.build(t, DefaultConfig(), nil)
	if rows[id].TargetPath == "" {
		t.Fatal("expected a target path derived from file mtime, got none")
	}
}

// TestCaptureGroupSidecarCoLocatesWithPair covers the real reported bug:
// an AAE sidecar has no EXIF of its own, so without grouping it would land
// wherever its own file mtime falls (a different day than the HEIC it
// belongs with — addFile gives every file the same fixed mtime, June 1,
// while the HEIC pair's real capture date is June 3). With the capture
// group, the sidecar inherits the pair's directory instead.
func TestCaptureGroupSidecarCoLocatesWithPair(t *testing.T) {
	h := newHarness(t)
	aae := h.addFile(t, "d/IMG_0042.AAE", "SIDECAR", classifier.CommonMetadata{})
	heic := h.addFile(t, "d/IMG_0042.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))
	edited := h.addFile(t, "d/IMG_E0042.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))

	rows := h.build(t, DefaultConfig(), nil)
	dirAAE := filepath.Dir(rows[aae].TargetPath)
	dirHEIC := filepath.Dir(rows[heic].TargetPath)
	dirEdited := filepath.Dir(rows[edited].TargetPath)
	if dirAAE != dirHEIC || dirHEIC != dirEdited {
		t.Errorf("capture bundle split across dirs: aae=%q heic=%q edited=%q", dirAAE, dirHEIC, dirEdited)
	}
}

// TestCaptureDirsForcesRawJpgTogetherAndKeepsRicherLocation is a direct unit
// test of captureDirs, deliberately bypassing Run()/clusterAndSuggest: the
// full pipeline's time-gap spillover (cluster.go's majorityCity) already
// fills in a same-moment file's missing location from a nearby member
// regardless of filename, so an end-to-end RAW+JPG test would pass even with
// captureDirs stubbed out — it wouldn't be testing this feature at all.
// Also guards the representative choice: the RAW lacks GPS, so picking it as
// leader (e.g. by insertion order — "IMG_2566.CR2" sorts before
// "IMG_2566.JPG") would drag the whole group into the location-less
// fallback and throw away the JPG's real location.
func TestCaptureDirsForcesRawJpgTogetherAndKeepsRicherLocation(t *testing.T) {
	takenAt := time.Date(2024, 6, 3, 10, 0, 0, 0, time.UTC)
	ts := "2024:06:03 10:00:00"
	masters := []masterFile{
		{FileDir: "/src/d", FileName: "IMG_2566.CR2", MediaType: classifier.MediaTypeRaw, takenAt: takenAt, DBDateTaken: new(ts)},
		{FileDir: "/src/d", FileName: "IMG_2566.JPG", MediaType: classifier.MediaTypeImage, takenAt: takenAt, DBDateTaken: new(ts), location: "Calangute"},
	}
	dirs := captureDirs(masters, nil, DefaultConfig())
	if len(dirs) != 2 || dirs[0] != dirs[1] {
		t.Fatalf("want RAW+JPG forced into one shared dir, got %v", dirs)
	}
	if !strings.Contains(dirs[0], "Calangute") {
		t.Errorf("group dir %q dropped the JPG's resolved location — RAW (no GPS) must not win leader over it", dirs[0])
	}
}

// TestCaptureGroupConflictingTimestampsStayIndependent is the anti-collision
// guard: two unrelated shoots reusing the same filename counter (e.g. an
// AirDropped file landing next to an existing series) must not merge just
// because the stem matches — only a same-second timestamp agreement groups.
func TestCaptureGroupConflictingTimestampsStayIndependent(t *testing.T) {
	h := newHarness(t)
	a := h.addFile(t, "d/IMG_2566.JPG", "IMAGE", metaWith("2019:03:01 10:00:00", 0, 0, 4000, 3000))
	b := h.addFile(t, "d/IMG_2566.CR2", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))

	rows := h.build(t, DefaultConfig(), nil)
	dirA := filepath.Dir(rows[a].TargetPath)
	dirB := filepath.Dir(rows[b].TargetPath)
	if dirA == dirB {
		t.Errorf("same-stem files with conflicting timestamps merged into %q — anti-collision guard should keep them independent", dirA)
	}
}

func TestCustomRulesOrder(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5439, 73.7553, 3024, 4032))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleMedia, RuleLocation}
	rows := h.build(t, cfg, geo)

	want := "2024/06_June/Calangute/IMG_0001.HEIC" // the lone Photos level collapses
	if rows[id].TargetPath != want {
		t.Errorf("target = %q, want %q", rows[id].TargetPath, want)
	}
}

// TestEmptyRulesIsFlatYearMonth covers the --group-by none CLI option
// (workflow translates that sentinel to an empty Rules slice before
// reaching here).
func TestEmptyRulesIsFlatYearMonth(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5439, 73.7553, 3024, 4032))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	cfg.Rules = nil
	rows := h.build(t, cfg, geo)

	want := "2024/06_June/IMG_0001.HEIC"
	if rows[id].TargetPath != want {
		t.Errorf("target = %q, want %q", rows[id].TargetPath, want)
	}
}

// addHomeAnchor sets a saved-place anchor on cfg for the given coords.
func addHomeAnchor(cfg *Config, name string, lat, lon float64) {
	cfg.Anchors = append(cfg.Anchors, location.Anchor{Name: name, FolderName: name, Lat: lat, Lon: lon})
}

// TestSavedPlacesDateOnly is the default behaviour: a photo taken at a confirmed
// saved place gets NO location level — it's grouped by date only, since
// there's no point sorting your everyday home photos into a town folder.
func TestSavedPlacesDateOnly(t *testing.T) {
	h := newHarness(t)
	// 28.58,77.33 is real Noida, ~14km (well within location.MaxDistSquared's
	// ~50km) from the Delhi anchor below
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 28.58, 77.33, 3024, 4032))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig() // SavedPlacesDateOnly defaults true
	addHomeAnchor(&cfg, "Delhi", 28.65195, 77.23149)
	cfg.Rules = []string{RuleLocation, RuleOrientation, RuleMedia}
	rows := h.build(t, cfg, geo)

	want := "2024/06_June/IMG_0001.HEIC"
	if rows[id].TargetPath != want {
		t.Errorf("target = %q, want %q (saved-place photo should be date-only, no location level)", rows[id].TargetPath, want)
	}
}

// TestSavedPlacesDateOnlySpilloverStaysSuppressed covers a reported bug: a
// GPS-less file clustered with GPS-tagged home photos (an indoor shot with no
// fix, taken minutes after ones that resolved to the home anchor) inherited
// the anchor's location string via clusterAndSuggest's spillover but not the
// atSavedPlace flag, so it alone leaked a location folder SavedPlacesDateOnly was
// supposed to suppress for the whole cluster.
func TestSavedPlacesDateOnlySpilloverStaysSuppressed(t *testing.T) {
	h := newHarness(t)
	withGPS := h.addFile(t, "d/h1.HEIC", "IMAGE", metaWith("2024:12:01 10:00:00", 22.7196, 75.8577, 3024, 4032))
	noGPS := h.addFile(t, "d/h2.HEIC", "IMAGE", metaWith("2024:12:01 10:05:00", 0, 0, 3024, 4032))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig() // SavedPlacesDateOnly defaults true
	addHomeAnchor(&cfg, "Indore", 22.7196, 75.8577)
	cfg.Rules = []string{RuleDate, RuleLocation}
	rows := h.build(t, cfg, geo)

	for _, id := range []int64{withGPS, noGPS} {
		want := "2024/12_December/01/" + filepath.Base(rows[id].TargetPath)
		if got := rows[id].TargetPath; got != want {
			t.Errorf("target = %q, want %q (spillover file should stay date-only too)", got, want)
		}
	}
}

// TestAnchorFoldsNearbySuburb covers the legacy opt-out (SavedPlacesDateOnly=false):
// a directly-resolved GPS location within location.MaxDistSquared of a confirmed
// saved-place label is replaced by that place's name, folding a metro's suburbs
// into one folder instead of fragmenting by neighbourhood.
func TestAnchorFoldsNearbySuburb(t *testing.T) {
	h := newHarness(t)
	// 28.58,77.33 is real Noida, ~14km (well within location.MaxDistSquared's
	// ~50km) from the Delhi anchor below
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 28.58, 77.33, 3024, 4032))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	addHomeAnchor(&cfg, "Delhi", 28.65195, 77.23149)
	cfg.SavedPlacesDateOnly = false // opt back into the suburb-fold behaviour
	cfg.Rules = []string{RuleLocation, RuleOrientation, RuleMedia}
	rows := h.build(t, cfg, geo)

	want := "2024/06_June/Delhi/IMG_0001.HEIC"
	if rows[id].TargetPath != want {
		t.Errorf("target = %q, want %q (anchor should fold the nearby suburb)", rows[id].TargetPath, want)
	}
}

// TestMergeSameLocationDays: three consecutive Goa days under one month collapse
// into a single dated range folder (default MergeSameLocationDays), while a
// different location on an interleaving day keeps its own day folder.
func TestMergeSameLocationDays(t *testing.T) {
	h := newHarness(t)
	// Goa on the 2nd, 3rd, 4th; Pune on the 3rd (same day, different place).
	// Calangute (15.5439,73.7553) and Pune (18.51957,73.85535) are both real,
	// unqualified geonames entries
	g2 := h.addFile(t, "d/g2.HEIC", "IMAGE", metaWith("2024:08:02 10:00:00", 15.5439, 73.7553, 3024, 4032))
	g3 := h.addFile(t, "d/g3.HEIC", "IMAGE", metaWith("2024:08:03 10:00:00", 15.5439, 73.7553, 3024, 4032))
	g4 := h.addFile(t, "d/g4.HEIC", "IMAGE", metaWith("2024:08:04 10:00:00", 15.5439, 73.7553, 3024, 4032))
	p3 := h.addFile(t, "d/p3.HEIC", "IMAGE", metaWith("2024:08:03 10:00:00", 18.51957, 73.85535, 3024, 4032))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation} // date above location
	rows := h.build(t, cfg, geo)

	for _, id := range []int64{g2, g3, g4} {
		want := "2024/08_August/02_04/Calangute/" + filepath.Base(rows[id].TargetPath)
		if rows[id].TargetPath != want {
			t.Errorf("Calangute target = %q, want %q (consecutive Calangute days should merge)", rows[id].TargetPath, want)
		}
	}
	if got := rows[p3].TargetPath; got != "2024/08_August/03/Pune/p3.HEIC" {
		t.Errorf("Pune target = %q, want 2024/08_August/03/Pune/p3.HEIC (interleaving location keeps its own day)", got)
	}
}

// TestMergeSameLocationDaysFoldsSavedPlacesDateOnly covers the reported bug:
// SavedPlacesDateOnly leaves atSavedPlace photos with no location at all, so
// mergeSameLocationDays's location=="" exclusion used to skip them outright —
// six consecutive home days rendered as six separate day folders instead of
// one range, even though they're all the same place same as a trip is.
func TestMergeSameLocationDaysFoldsSavedPlacesDateOnly(t *testing.T) {
	h := newHarness(t)
	ids := make([]int64, 0, 6)
	for d := 1; d <= 6; d++ {
		ids = append(ids, h.addFile(t, fmt.Sprintf("d/h%d.HEIC", d), "IMAGE",
			metaWith(fmt.Sprintf("2024:12:%02d 10:00:00", d), 22.7196, 75.8577, 3024, 4032)))
	}
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	addHomeAnchor(&cfg, "Indore", 22.7196, 75.8577)
	cfg.Rules = []string{RuleDate, RuleLocation}
	// SavedPlacesDateOnly defaults true — the reported config
	rows := h.build(t, cfg, geo)

	for _, id := range ids {
		want := "2024/12_December/01_06/" + filepath.Base(rows[id].TargetPath)
		if got := rows[id].TargetPath; got != want {
			t.Errorf("target = %q, want %q (consecutive home days should merge with no location folder)", got, want)
		}
	}
}

func TestBuildManyFiles(t *testing.T) {
	h := newHarness(t)
	const n = 60
	ids := make([]int64, 0, n)
	for i := range n {
		relPath := fmt.Sprintf("d/IMG_%04d.HEIC", i)
		ids = append(ids, h.addFile(t, relPath, "IMAGE",
			metaWith(fmt.Sprintf("2024:06:03 %02d:%02d:00", 10+i/60, i%60), 15.5439, 73.7553, 3024, 4032)))
	}
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	rows := h.build(t, cfg, geo)
	if len(rows) != n {
		t.Fatalf("entries = %d, want %d", len(rows), n)
	}
	for _, id := range ids {
		if rows[id].TargetPath == "" {
			t.Errorf("file %d has empty target", id)
		}
	}
}

func TestStripOffset(t *testing.T) {
	tests := []struct{ in, want string }{
		{"2024:06:03 14:00:00+05:30", "2024:06:03 14:00:00"},
		{"2024:06:03 14:00:00-07:00", "2024:06:03 14:00:00"},
		{"2024:06:03 14:00:00Z", "2024:06:03 14:00:00"},
		{"2024:06:03 14:00:00", "2024:06:03 14:00:00"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := stripOffset(tc.in); got != tc.want {
			t.Errorf("stripOffset(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTakenAtPrefersCreationDateOverCreateDateForVideo covers a real reported
// bug: a video's CreateDate is QuickTime's raw (UTC, no offset) timestamp,
// which reads hours off from a photo taken at the exact same moment. iOS
// videos also carry CreationDate, which does have an offset — stripped (not
// applied, since every sibling timestamp here is naive local wall-clock) it
// lines the video's derived time back up with photos from the same moment,
// instead of shifting it into a different day/cluster.
func TestTakenAtPrefersCreationDateOverCreateDateForVideo(t *testing.T) {
	createDate := "2024:06:03 08:30:00"         // QuickTime's raw UTC CreateDate
	creationDate := "2024:06:03 14:00:00+05:30" // same real instant, offset-aware
	masters := []masterFile{{DBCreateDate: &createDate, DBCreationDate: &creationDate}}

	deriveAll(masters)

	want := time.Date(2024, time.June, 3, 14, 0, 0, 0, time.UTC)
	if !masters[0].takenAt.Equal(want) {
		t.Errorf("takenAt = %v, want %v (CreationDate's wall-clock time, offset stripped, not CreateDate's UTC-shifted one)",
			masters[0].takenAt, want)
	}
}

func TestOrientationTagSwapsDimensions(t *testing.T) {
	h := newHarness(t)
	// stored landscape (4032x3024) but Orientation 6 = rotated 90° → viewed vertical
	meta := metaWith("2024:06:03 14:00:00", 15.5439, 73.7553, 4032, 3024)
	meta.Orientation = "6"
	id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", meta)
	// a genuinely horizontal second file, or the orientation level would be
	// collapsed away (one distinct value library-wide) and this would assert
	// nothing about orientation at all
	h.addFile(t, "d/IMG_0002.HEIC", "IMAGE", metaWith("2024:06:03 15:00:00", 15.5439, 73.7553, 4032, 3024))
	geo := installtest.Resolver(t)

	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation, RuleOrientation, RuleMedia}
	rows := h.build(t, cfg, geo)
	// media still collapses (both files are photos); orientation survives
	want := "2024/06_June/Calangute/Vertical/IMG_0001.HEIC"
	if rows[id].TargetPath != want {
		t.Errorf("target = %q, want %q", rows[id].TargetPath, want)
	}
}

func TestCollisionWithLiteralSuffixStem(t *testing.T) {
	h := newHarness(t)
	// d3's real stem IMG_1_2 must not be clobbered by d2's collision suffix
	a := h.addFile(t, "d1/IMG_1.JPG", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))
	b := h.addFile(t, "d2/IMG_1.JPG", "IMAGE", metaWith("2024:06:03 10:05:00", 0, 0, 4000, 3000))
	c := h.addFile(t, "d3/IMG_1_2.JPG", "IMAGE", metaWith("2024:06:03 10:10:00", 0, 0, 4000, 3000))

	rows := h.build(t, DefaultConfig(), nil)
	paths := map[string]bool{}
	for _, id := range []int64{a, b, c} {
		if paths[rows[id].TargetPath] {
			t.Errorf("duplicate target %q", rows[id].TargetPath)
		}
		paths[rows[id].TargetPath] = true
	}
}

func TestRebuildRemovesStaleEntries(t *testing.T) {
	h := newHarness(t)
	geo := installtest.Resolver(t)
	keep := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5439, 73.7553, 3024, 4032))
	gone := h.addFile(t, "d/IMG_0002.HEIC", "IMAGE", metaWith("2024:06:03 15:00:00", 15.5439, 73.7553, 3024, 4032))
	h.build(t, DefaultConfig(), geo)

	// second file loses master status between builds (e.g. re-scored)
	if _, err := h.d.ExecContext(context.Background(),
		`UPDATE file_metadata SET is_master = 0 WHERE file_id = ?`, gone); err != nil {
		t.Fatal(err)
	}
	rows := h.build(t, DefaultConfig(), geo)
	if len(rows) != 1 {
		t.Fatalf("entries after rebuild = %d, want 1", len(rows))
	}
	if _, ok := rows[keep]; !ok {
		t.Errorf("surviving master missing from rebuild")
	}
}

func TestRebuildIsIdempotent(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5439, 73.7553, 3024, 4032))
	geo := installtest.Resolver(t)

	first := h.build(t, DefaultConfig(), geo)
	second := h.build(t, DefaultConfig(), geo)
	if len(second) != 1 || second[id].TargetPath != first[id].TargetPath {
		t.Errorf("rebuild changed entries: %v vs %v", first[id], second[id])
	}
}

// TestLibraryScopeAcrossRuns covers the incremental re-scan contract: a
// master indexed by an earlier run is still proposed by a later run, and the
// later run replaces the previous proposal set wholesale
func TestLibraryScopeAcrossRuns(t *testing.T) {
	h := newHarness(t)
	// File already indexed by an earlier run…
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	// …with a stale proposal that earlier run persisted
	if _, err := h.d.ExecContext(context.Background(), `
		INSERT INTO virtual_fs_entries (file_id, source_path, target_path)
		VALUES (?, '/src/dump/IMG_0001.HEIC', 'stale/IMG_0001.HEIC')`,
		id); err != nil {
		t.Fatal(err)
	}

	vfs := &VFS{
		db:  h.d,
		log: logger.NewNoopLogger(),
		cfg: DefaultConfig(),
	}
	count, err := vfs.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("Run proposed %d entries, want 1 (earlier-run master must be included)", count)
	}
	h.d.Writer.Flush()

	var rows []entryRow
	if err := h.d.SQL.Select(&rows,
		`SELECT file_id, target_path, cluster_id, status, suggestion, suggestion_source FROM virtual_fs_entries`); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("library has %d proposal rows, want exactly 1 (stale rows wiped)", len(rows))
	}
	if rows[0].FileID != id {
		t.Errorf("proposal file_id = %d, want %d", rows[0].FileID, id)
	}
	if rows[0].TargetPath == "stale/IMG_0001.HEIC" {
		t.Error("stale proposal survived the rebuild")
	}
}

// TestMonthFoldersSortChronologically covers the reported ordering bug: bare
// month names sort alphabetically, so December came before November in the
// review tree (and in any file browser). The segment is number-first now.
func TestMonthFoldersSortChronologically(t *testing.T) {
	months := []string{}
	for m := time.January; m <= time.December; m++ {
		months = append(months, time.Date(2025, m, 15, 12, 0, 0, 0, time.UTC).Format("01_January"))
	}
	if !sort.StringsAreSorted(months) {
		t.Errorf("month segments %v are not in lexicographic order — they must be, since that's how both the review tree and the OS list them", months)
	}
}

// TestDateLevelAddsDayFolder covers the Year/Month/Day/Location/... shape:
// "date" is a real group-by level, placed wherever the user puts it.
func TestDateLevelAddsDayFolder(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5439, 73.7553, 3024, 4032))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	rows := h.build(t, cfg, installtest.Resolver(t))

	want := "2024/06_June/03/Calangute/IMG_0001.HEIC"
	if got := rows[id].TargetPath; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
}

// TestUnknownLocationEmitsNoLocationFolder covers both halves of the reported
// bug: with a Day level the dated placeholder rung is skipped (it produced a
// second date, "…/03/03-05/"), and there is no device rung below it either —
// a location folder named after the camera is wrong information, and it
// duplicated the device level right next to it.
func TestUnknownLocationEmitsNoLocationFolder(t *testing.T) {
	h := newHarness(t)
	a := h.addFile(t, "dump/DSC_0001.JPG", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation, RuleDevice}
	rows := h.build(t, cfg, installtest.Resolver(t))

	// device level collapses too (one value library-wide), so the day is all
	// that is left — the point is that no folder claims to be a location
	want := "2024/06_June/03/DSC_0001.JPG"
	if got := rows[a].TargetPath; got != want {
		t.Errorf("target = %q, want %q (no location known, so no location folder)", got, want)
	}

	var withDir int
	if err := h.d.SQL.Get(&withDir,
		`SELECT COUNT(*) FROM virtual_fs_entries WHERE suggestion_dir IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if withDir != 0 {
		t.Errorf("%d rows carry a suggestion_dir, want 0 — there is no location folder to hang a suggestion on", withDir)
	}
}

// TestSuggestionDirTracksTheLocationLevel covers the misplaced-suggestion bug:
// with location second, the suggestion must attach to the location folder, not
// to whatever sits at the old hardcoded depth 2 (here, the Day).
func TestSuggestionDirTracksTheLocationLevel(t *testing.T) {
	h := newHarness(t)
	h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5439, 73.7553, 3024, 4032))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	h.build(t, cfg, installtest.Resolver(t))

	var dirs []string
	if err := h.d.SQL.Select(&dirs,
		`SELECT suggestion_dir FROM virtual_fs_entries`); err != nil {
		t.Fatal(err)
	}
	want := "2024/06_June/03/Calangute"
	if len(dirs) != 1 || dirs[0] != want {
		t.Errorf("suggestion_dir = %v, want [%q] — the location folder, not the Day", dirs, want)
	}
}

// TestCollapsesUninformativeLevels covers the reported case verbatim: every
// file is a vertical iPhone shot, so the device and orientation folders never
// distinguish anything and are dropped — but Photos/Videos survives, because
// the library really does have both.
func TestCollapsesUninformativeLevels(t *testing.T) {
	h := newHarness(t)
	photo := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5439, 73.7553, 3024, 4032))
	video := h.addFile(t, "dump/IMG_0002.MOV", "VIDEO", metaWith("2024:06:03 11:00:00", 15.5439, 73.7553, 3024, 4032))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation, RuleDevice, RuleOrientation, RuleMedia}
	rows := h.build(t, cfg, installtest.Resolver(t))

	for id, want := range map[int64]string{
		photo: "2024/06_June/03/Calangute/Photos/IMG_0001.HEIC",
		video: "2024/06_June/03/Calangute/Videos/IMG_0002.MOV",
	} {
		if got := rows[id].TargetPath; got != want {
			t.Errorf("target = %q, want %q", got, want)
		}
	}
}

// TestCollapseKeepsDateAndLocation covers the exemption: even with one day and
// one city in the whole library, those folders stay — they're how a person
// recognizes the folder, and merging days is the review TUI's job.
func TestCollapseKeepsDateAndLocation(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5439, 73.7553, 3024, 4032))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation, RuleMedia}
	rows := h.build(t, cfg, installtest.Resolver(t))

	want := "2024/06_June/03/Calangute/IMG_0001.HEIC"
	if got := rows[id].TargetPath; got != want {
		t.Errorf("target = %q, want %q (day and city kept, lone Photos dropped)", got, want)
	}
}

// TestCollapseReversesWhenALaterScanAddsAVideo covers the self-correcting
// rebuild: the media level was dropped while the library was photo-only, and
// must come back — with the existing photo moved inside it — as soon as a
// video shows up. Nothing special does this; vfs.Run re-proposes the whole
// library every time.
func TestCollapseReversesWhenALaterScanAddsAVideo(t *testing.T) {
	h := newHarness(t)
	photo := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5439, 73.7553, 3024, 4032))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation, RuleMedia}
	geo := installtest.Resolver(t)

	rows := h.build(t, cfg, geo)
	if got, want := rows[photo].TargetPath, "2024/06_June/Calangute/IMG_0001.HEIC"; got != want {
		t.Fatalf("before: target = %q, want %q", got, want)
	}

	h.addFile(t, "dump/IMG_0002.MOV", "VIDEO", metaWith("2024:06:03 11:00:00", 15.5439, 73.7553, 3024, 4032))
	rows = h.build(t, cfg, geo)
	if got, want := rows[photo].TargetPath, "2024/06_June/Calangute/Photos/IMG_0001.HEIC"; got != want {
		t.Errorf("after: target = %q, want %q (Photos comes back and the photo moves into it)", got, want)
	}
}

// TestCollapseDisabled covers the collapse-levels escape hatch: the full
// nesting is proposed even where a level has one value.
func TestCollapseDisabled(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5439, 73.7553, 3024, 4032))
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleLocation, RuleOrientation, RuleMedia}
	cfg.CollapseLevels = false
	rows := h.build(t, cfg, installtest.Resolver(t))

	want := "2024/06_June/Calangute/Vertical/Photos/IMG_0001.HEIC"
	if got := rows[id].TargetPath; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
}
