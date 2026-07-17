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
	"testing"

	"github.com/google/uuid"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// fakeGeo resolves every coordinate to a fixed city per rough lat bucket
type fakeGeo struct {
	cities map[int]string // int(lat) → city
}

func (f *fakeGeo) Lookup(_ context.Context, lat, _ float64) (string, error) {
	if c, ok := f.cities[int(lat)]; ok {
		return c, nil
	}
	return "", fmt.Errorf("not found")
}

type entryRow struct {
	FileID           int64   `db:"file_id"`
	TargetPath       string  `db:"target_path"`
	ClusterID        *string `db:"cluster_id"`
	Status           string  `db:"status"`
	Suggestion       *string `db:"suggestion"`
	SuggestionSource *string `db:"suggestion_source"`
}

type harness struct {
	d         *db.DB
	sessionID uuid.UUID
	nextID    int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	d := dbtest.New(t)
	return &harness{d: d, sessionID: dbtest.NewSession(t, d, db.StatusScored)}
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
			scan_session_id, file_extension, media_type, discovered_at, last_seen_at)
		VALUES (?, ?, ?, 1024, '2024-06-01T10:00:00.000000000Z', ?, ?, ?,
			'2024-06-01T10:00:00.000000000Z', '2024-06-01T10:00:00.000000000Z')`,
		id, dir, name, h.sessionID.String(), filepath.Ext(name), mediaType); err != nil {
		t.Fatal(err)
	}
	if _, err := h.d.ExecContext(context.Background(), `
		INSERT INTO file_metadata (file_hash, file_id,
			exif_image_width, exif_image_height, exif_orientation,
			exif_gps_latitude, exif_gps_longitude, exif_make, exif_model,
			exif_date_time_original, exif_create_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fmt.Sprintf("hash-%d", id), id,
		db.IntOrNil(meta.ImageWidth), db.IntOrNil(meta.ImageHeight), db.IntOrNil(meta.Orientation),
		db.FloatOrNil(meta.GPSLatitude), db.FloatOrNil(meta.GPSLongitude),
		db.StrOrNil(meta.Make), db.StrOrNil(meta.Model),
		db.StrOrNil(meta.DateTimeOriginal), db.StrOrNil(meta.CreateDate)); err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *harness) build(t *testing.T, cfg Config, geo geoResolver) map[int64]entryRow {
	t.Helper()
	vfs := &VFS{
		db:  h.d,
		log: logger.NewNoopLogger(),
		cfg: cfg,
	}
	if geo != nil {
		vfs.resolver = geo
	}
	if _, err := vfs.Run(context.Background(), h.sessionID); err != nil {
		t.Fatal(err)
	}
	h.d.Writer.Flush()

	var rows []entryRow
	if err := h.d.SQL.Select(&rows, `
		SELECT file_id, target_path, cluster_id, status, suggestion, suggestion_source
		FROM virtual_fs_entries WHERE session_id = ?`, h.sessionID.String()); err != nil {
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
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5, 73.8, 3024, 4032))
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

	rows := h.build(t, DefaultConfig(), geo)

	want := "2024/06_June/Goa/Vertical/Photos/IMG_0001.HEIC"
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

func TestClusterSpillover(t *testing.T) {
	h := newHarness(t)
	// two files without GPS, one with — all within the 12h gap
	a := h.addFile(t, "dump/DSC_0001.JPG", "IMAGE", metaWith("2024:06:03 10:00:00", 0, 0, 4000, 3000))
	b := h.addFile(t, "dump/DSC_0002.JPG", "IMAGE", metaWith("2024:06:03 12:00:00", 0, 0, 4000, 3000))
	c := h.addFile(t, "dump/IMG_0003.HEIC", "IMAGE", metaWith("2024:06:03 11:00:00", 32.2, 77.1, 3024, 4032))
	geo := &fakeGeo{cities: map[int]string{32: "Manali"}}

	rows := h.build(t, DefaultConfig(), geo)

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

	wantA := "2024/06_June/Jun_03/Horizontal/Photos/DSC_0001.JPG"
	wantB := "2024/06_June/Jun_05/Horizontal/Photos/DSC_0002.JPG"
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
	// label casing is user-chosen and must survive NameCase normalisation
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

func TestAnchorSuggestion(t *testing.T) {
	h := newHarness(t)
	// hometown files with GPS establish the anchor…
	h.addFile(t, "home/IMG_0001.HEIC", "IMAGE", metaWith("2024:05:01 10:00:00", 22.7, 75.8, 3024, 4032))
	h.addFile(t, "home/IMG_0002.HEIC", "IMAGE", metaWith("2024:05:02 10:00:00", 22.7, 75.8, 3024, 4032))
	// …and a far-later unlocated cluster gets it suggested
	u := h.addFile(t, "dump/DSC_0009.JPG", "IMAGE", metaWith("2024:06:20 10:00:00", 0, 0, 4000, 3000))
	geo := &fakeGeo{cities: map[int]string{22: "Indore"}}

	rows := h.build(t, DefaultConfig(), geo)
	if rows[u].Suggestion == nil || *rows[u].Suggestion != "Indore" {
		t.Errorf("suggestion = %v, want 'Indore'", rows[u].Suggestion)
	}
	if rows[u].SuggestionSource == nil || *rows[u].SuggestionSource != SuggestionAnchor {
		t.Errorf("suggestion_source = %v, want ANCHOR", rows[u].SuggestionSource)
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
	a := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 22.7, 75.8, 3024, 4032))
	b := h.addFile(t, "d/IMG_0002.HEIC", "IMAGE", metaWith("2024:06:20 10:00:00", 22.7, 75.8, 3024, 4032))
	geo := &fakeGeo{cities: map[int]string{22: "Indore"}}

	rows := h.build(t, DefaultConfig(), geo)
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

func TestCaptureGroupMovesTogether(t *testing.T) {
	h := newHarness(t)
	// Live Photo pair: HEIC original + MOV live video share the capture stem
	a := h.addFile(t, "d/IMG_0042.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5, 73.8, 3024, 4032))
	b := h.addFile(t, "d/IMG_0042.MOV", "VIDEO", metaWith("2024:06:03 10:00:00", 15.5, 73.8, 1920, 1080))
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

	rows := h.build(t, DefaultConfig(), geo)
	dirA := filepath.Dir(rows[a].TargetPath)
	dirB := filepath.Dir(rows[b].TargetPath)
	if dirA != dirB {
		t.Errorf("capture group split across %q and %q", dirA, dirB)
	}
}

func TestCustomSlotOrder(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5, 73.8, 3024, 4032))
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

	cfg := DefaultConfig()
	cfg.Slots = []string{SlotMedia, SlotLocation}
	rows := h.build(t, cfg, geo)

	want := "2024/06_June/Photos/Goa/IMG_0001.HEIC"
	if rows[id].TargetPath != want {
		t.Errorf("target = %q, want %q", rows[id].TargetPath, want)
	}
}

func TestBuildManyFiles(t *testing.T) {
	h := newHarness(t)
	const n = 60
	ids := make([]int64, 0, n)
	for i := range n {
		relPath := fmt.Sprintf("d/IMG_%04d.HEIC", i)
		ids = append(ids, h.addFile(t, relPath, "IMAGE",
			metaWith(fmt.Sprintf("2024:06:03 %02d:%02d:00", 10+i/60, i%60), 15.5, 73.8, 3024, 4032)))
	}
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

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

func TestNameCase(t *testing.T) {
	cases := []struct {
		style string
		want  string
	}{
		{CaseTitle, "Goa Beach"},
		{CaseLower, "goa beach"},
		{CaseUpper, "GOA BEACH"},
		{CaseAsIs, "goa BEACH"},
	}
	for _, tc := range cases {
		t.Run(tc.style, func(t *testing.T) {
			h := newHarness(t)
			id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5, 73.8, 3024, 4032))
			geo := &fakeGeo{cities: map[int]string{15: "goa BEACH"}}

			cfg := DefaultConfig()
			cfg.NameCase = tc.style
			rows := h.build(t, cfg, geo)

			want := "2024/06_June/" + tc.want + "/Vertical/Photos/IMG_0001.HEIC"
			if rows[id].TargetPath != want {
				t.Errorf("target = %q, want %q", rows[id].TargetPath, want)
			}
		})
	}
}

func TestOrientationTagSwapsDimensions(t *testing.T) {
	h := newHarness(t)
	// stored landscape (4032x3024) but Orientation 6 = rotated 90° → viewed vertical
	meta := metaWith("2024:06:03 14:00:00", 15.5, 73.8, 4032, 3024)
	meta.Orientation = "6"
	id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", meta)
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

	rows := h.build(t, DefaultConfig(), geo)
	want := "2024/06_June/Goa/Vertical/Photos/IMG_0001.HEIC"
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
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}
	keep := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5, 73.8, 3024, 4032))
	gone := h.addFile(t, "d/IMG_0002.HEIC", "IMAGE", metaWith("2024:06:03 15:00:00", 15.5, 73.8, 3024, 4032))
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
	id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5, 73.8, 3024, 4032))
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

	first := h.build(t, DefaultConfig(), geo)
	second := h.build(t, DefaultConfig(), geo)
	if len(second) != 1 || second[id].TargetPath != first[id].TargetPath {
		t.Errorf("rebuild changed entries: %v vs %v", first[id], second[id])
	}
}

// TestLibraryScopeAcrossSessions covers the incremental re-scan contract: a
// master indexed by an earlier session is still proposed by a later run, and
// the later run replaces the previous proposal set wholesale
func TestLibraryScopeAcrossSessions(t *testing.T) {
	h := newHarness(t)
	// File belongs to the harness's (old) session…
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 0, 0, 3024, 4032))
	// …with a stale proposal persisted by that old session
	if _, err := h.d.ExecContext(context.Background(), `
		INSERT INTO virtual_fs_entries (session_id, file_id, source_path, target_path)
		VALUES (?, ?, '/src/dump/IMG_0001.HEIC', 'stale/IMG_0001.HEIC')`,
		h.sessionID.String(), id); err != nil {
		t.Fatal(err)
	}

	// A brand-new session runs the VFS phase without having indexed anything
	newSession := uuid.New()
	if _, err := h.d.ExecContext(context.Background(),
		`INSERT INTO scan_sessions (id, status, root_paths) VALUES (?, 'SCORED', '/src')`,
		newSession.String()); err != nil {
		t.Fatal(err)
	}
	vfs := &VFS{
		db:  h.d,
		log: logger.NewNoopLogger(),
		cfg: DefaultConfig(),
	}
	count, err := vfs.Run(context.Background(), newSession)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("Run proposed %d entries, want 1 (old-session master must be included)", count)
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
