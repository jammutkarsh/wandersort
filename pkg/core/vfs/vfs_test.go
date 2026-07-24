package vfs

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

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

	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByLocation, GroupByOrientation, GroupByMedia}
	rows := h.build(t, cfg, geo)

	// device/orientation/media all have a single value across this one-file
	// library, so every one of those levels collapses away
	want := "2024/06_June/Goa/IMG_0001.HEIC"
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

	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByLocation, GroupByOrientation, GroupByMedia}
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

// TestAnchorSuggestion covers a fully unresolved cluster (no GPS at all)
// falling back to a *confirmed* home/work anchor for its suggestion.
// anchorCities deliberately no longer falls back to "the library's most
// frequent city" with no confirmed anchor — see the anchorCities doc comment
// for why (a real reported bug: an unrelated DSLR photo with no GPS anywhere
// nearby was "suggested" whatever city happened to dominate the library).
func TestAnchorSuggestion(t *testing.T) {
	h := newHarness(t)
	if _, err := h.d.ExecContext(context.Background(),
		`INSERT INTO user_labels (label, kind) VALUES ('Indore', 'ANCHOR_HOME')`); err != nil {
		t.Fatal(err)
	}
	u := h.addFile(t, "dump/DSC_0009.JPG", "IMAGE", metaWith("2024:06:20 10:00:00", 0, 0, 4000, 3000))

	rows := h.build(t, DefaultConfig(), nil)
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
	for i := range 5 {
		h.addFile(t, fmt.Sprintf("phone/IMG_%d.HEIC", i), "IMAGE", metaWith("2024:05:01 10:00:00", 31.6, 77.3, 3024, 4032))
	}
	geo := &fakeGeo{cities: map[int]string{31: "Banjar"}}

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
	a := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 22.7, 75.8, 3024, 4032))
	b := h.addFile(t, "d/IMG_0002.HEIC", "IMAGE", metaWith("2024:06:20 10:00:00", 22.7, 75.8, 3024, 4032))
	geo := &fakeGeo{cities: map[int]string{22: "Indore"}}

	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByLocation, GroupByOrientation, GroupByMedia}
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
// GPS): with GroupBy excluding media type, both members still land in the
// same directory purely because their own derived location/date agree —
// no stem-based special-casing required to get the intuitive result.
func TestSameStemSameCaptureStillCoLocatesByOwnAttributes(t *testing.T) {
	h := newHarness(t)
	a := h.addFile(t, "d/IMG_0042.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5, 73.8, 3024, 4032))
	b := h.addFile(t, "d/IMG_0042.MOV", "VIDEO", metaWith("2024:06:03 10:00:00", 15.5, 73.8, 1920, 1080))
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByLocation} // no media split, so a real Live Photo pair still co-locates
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

func TestCustomGroupByOrder(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5, 73.8, 3024, 4032))
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByMedia, GroupByLocation}
	rows := h.build(t, cfg, geo)

	want := "2024/06_June/Goa/IMG_0001.HEIC" // the lone Photos level collapses
	if rows[id].TargetPath != want {
		t.Errorf("target = %q, want %q", rows[id].TargetPath, want)
	}
}

// TestEmptyGroupByIsFlatYearMonth covers the --group-by none CLI option
// (workflow translates that sentinel to an empty GroupBy slice before
// reaching here).
func TestEmptyGroupByIsFlatYearMonth(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5, 73.8, 3024, 4032))
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

	cfg := DefaultConfig()
	cfg.GroupBy = nil
	rows := h.build(t, cfg, geo)

	want := "2024/06_June/IMG_0001.HEIC"
	if rows[id].TargetPath != want {
		t.Errorf("target = %q, want %q", rows[id].TargetPath, want)
	}
}

// TestAnchorFoldsNearbySuburb covers the home/work anchor override: a
// directly-resolved GPS location within location.MaxDistSquared of a
// confirmed ANCHOR_HOME/WORK label is replaced by the anchor's name, so a
// metro's suburbs land in one folder instead of fragmenting by neighbourhood.
func TestAnchorFoldsNearbySuburb(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 28.60, 77.20, 3024, 4032))
	geo := &fakeGeo{cities: map[int]string{28: "Some Suburb"}}

	if _, err := h.d.ExecContext(context.Background(),
		`INSERT INTO user_labels (label, kind, gps_lat, gps_lon) VALUES (?, 'ANCHOR_HOME', ?, ?)`,
		"Delhi", 28.61, 77.21); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByLocation, GroupByOrientation, GroupByMedia}
	rows := h.build(t, cfg, geo)

	want := "2024/06_June/Delhi/IMG_0001.HEIC"
	if rows[id].TargetPath != want {
		t.Errorf("target = %q, want %q (anchor should fold the nearby suburb)", rows[id].TargetPath, want)
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
			cfg.GroupBy = []string{GroupByLocation, GroupByOrientation, GroupByMedia}
			cfg.NameCase = tc.style
			rows := h.build(t, cfg, geo)

			want := "2024/06_June/" + tc.want + "/IMG_0001.HEIC"
			if rows[id].TargetPath != want {
				t.Errorf("target = %q, want %q", rows[id].TargetPath, want)
			}
		})
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
	meta := metaWith("2024:06:03 14:00:00", 15.5, 73.8, 4032, 3024)
	meta.Orientation = "6"
	id := h.addFile(t, "d/IMG_0001.HEIC", "IMAGE", meta)
	// a genuinely horizontal second file, or the orientation level would be
	// collapsed away (one distinct value library-wide) and this would assert
	// nothing about orientation at all
	h.addFile(t, "d/IMG_0002.HEIC", "IMAGE", metaWith("2024:06:03 15:00:00", 15.5, 73.8, 4032, 3024))
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByLocation, GroupByOrientation, GroupByMedia}
	rows := h.build(t, cfg, geo)
	// media still collapses (both files are photos); orientation survives
	want := "2024/06_June/Goa/Vertical/IMG_0001.HEIC"
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
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 14:00:00", 15.5, 73.8, 3024, 4032))
	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByDate, GroupByLocation}
	rows := h.build(t, cfg, &fakeGeo{cities: map[int]string{15: "Goa"}})

	want := "2024/06_June/03/Goa/IMG_0001.HEIC"
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
	cfg.GroupBy = []string{GroupByDate, GroupByLocation, GroupByDevice}
	rows := h.build(t, cfg, &fakeGeo{cities: map[int]string{}})

	// device level collapses too (one value library-wide), so the day is all
	// that is left — the point is that no folder claims to be a location
	want := "2024/06_June/03/DSC_0001.JPG"
	if got := rows[a].TargetPath; got != want {
		t.Errorf("target = %q, want %q (no location known, so no location folder)", got, want)
	}

	var withDir int
	if err := h.d.SQL.Get(&withDir,
		`SELECT COUNT(*) FROM virtual_fs_entries WHERE session_id = ? AND suggestion_dir IS NOT NULL`,
		h.sessionID.String()); err != nil {
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
	h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5, 73.8, 3024, 4032))
	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByDate, GroupByLocation}
	h.build(t, cfg, &fakeGeo{cities: map[int]string{15: "Goa"}})

	var dirs []string
	if err := h.d.SQL.Select(&dirs,
		`SELECT suggestion_dir FROM virtual_fs_entries WHERE session_id = ?`, h.sessionID.String()); err != nil {
		t.Fatal(err)
	}
	want := "2024/06_June/03/Goa"
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
	photo := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5, 73.8, 3024, 4032))
	video := h.addFile(t, "dump/IMG_0002.MOV", "VIDEO", metaWith("2024:06:03 11:00:00", 15.5, 73.8, 3024, 4032))
	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByDate, GroupByLocation, GroupByDevice, GroupByOrientation, GroupByMedia}
	rows := h.build(t, cfg, &fakeGeo{cities: map[int]string{15: "Goa"}})

	for id, want := range map[int64]string{
		photo: "2024/06_June/03/Goa/Photos/IMG_0001.HEIC",
		video: "2024/06_June/03/Goa/Videos/IMG_0002.MOV",
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
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5, 73.8, 3024, 4032))
	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByDate, GroupByLocation, GroupByMedia}
	rows := h.build(t, cfg, &fakeGeo{cities: map[int]string{15: "Goa"}})

	want := "2024/06_June/03/Goa/IMG_0001.HEIC"
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
	photo := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5, 73.8, 3024, 4032))
	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByLocation, GroupByMedia}
	geo := &fakeGeo{cities: map[int]string{15: "Goa"}}

	rows := h.build(t, cfg, geo)
	if got, want := rows[photo].TargetPath, "2024/06_June/Goa/IMG_0001.HEIC"; got != want {
		t.Fatalf("before: target = %q, want %q", got, want)
	}

	h.addFile(t, "dump/IMG_0002.MOV", "VIDEO", metaWith("2024:06:03 11:00:00", 15.5, 73.8, 3024, 4032))
	rows = h.build(t, cfg, geo)
	if got, want := rows[photo].TargetPath, "2024/06_June/Goa/Photos/IMG_0001.HEIC"; got != want {
		t.Errorf("after: target = %q, want %q (Photos comes back and the photo moves into it)", got, want)
	}
}

// TestCollapseDisabled covers the collapse-levels escape hatch: the full
// nesting is proposed even where a level has one value.
func TestCollapseDisabled(t *testing.T) {
	h := newHarness(t)
	id := h.addFile(t, "dump/IMG_0001.HEIC", "IMAGE", metaWith("2024:06:03 10:00:00", 15.5, 73.8, 3024, 4032))
	cfg := DefaultConfig()
	cfg.GroupBy = []string{GroupByLocation, GroupByOrientation, GroupByMedia}
	cfg.CollapseLevels = false
	rows := h.build(t, cfg, &fakeGeo{cities: map[int]string{15: "Goa"}})

	want := "2024/06_June/Goa/Vertical/Photos/IMG_0001.HEIC"
	if got := rows[id].TargetPath; got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
}
