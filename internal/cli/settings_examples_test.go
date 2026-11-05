package cli

import (
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
)

func newTestExamples(t *testing.T, selected []string, collapse, mergeDays, dateOnly bool, home string) *settingsExamples {
	t.Helper()
	return newSettingsExamples(func() []string { return selected }, &collapse, &mergeDays, &dateOnly, &home)
}

func TestPreviewRulesOnlyAddsCollapsibleLevels(t *testing.T) {
	e := newTestExamples(t, []string{vfs.RuleDevice, vfs.RuleMedia}, false, false, false, "")
	got := e.previewRules(vfs.RuleDate, vfs.RuleLocation)
	want := []string{vfs.RuleDate, vfs.RuleLocation, vfs.RuleDevice, vfs.RuleMedia}
	if len(got) != len(want) {
		t.Fatalf("previewRules() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("previewRules()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHomeTown(t *testing.T) {
	tests := []struct {
		name, home, want string
	}{
		{"blank falls back to Indore", "", "Indore"},
		{"whitespace falls back to Indore", "   ", "Indore"},
		{"plain city name", "Pune", "Pune"},
		{"qualified name takes only the city", "Hyderabad, Telangana, India", "Hyderabad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestExamples(t, nil, false, false, false, tt.home)
			if got := e.homeTown(); got != tt.want {
				t.Errorf("homeTown() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRulesExample(t *testing.T) {
	e := newTestExamples(t, []string{vfs.RuleDate, vfs.RuleLocation}, false, false, false, "")
	got := e.Rules()
	if !strings.Contains(got, "Goa") {
		t.Errorf("Rules() example = %q, want it to show the sample's Goa location", got)
	}
}

// A yes/no description says what the question does and stays put as the answer changes.
func TestYesNoDescriptionsDoNotFollowTheAnswer(t *testing.T) {
	e := newTestExamples(t, nil, true, true, true, "Pune")
	describers := map[string]func() string{
		"collapse": e.CollapseDescribe, "dateOnly": e.DateOnlyDescribe, "mergeDays": e.MergeDaysDescribe,
	}
	for name, describe := range describers {
		before := describe()
		*e.collapse, *e.dateOnly, *e.mergeDays = false, false, false
		after := describe()
		if before != after {
			t.Errorf("%s: description changed with the answer:\n%q\n%q", name, before, after)
		}
		if strings.TrimSpace(after) == "" || strings.Contains(after, "Yes:") {
			t.Errorf("%s: description = %q, want one plain sentence about what the question does", name, after)
		}
		*e.collapse, *e.dateOnly, *e.mergeDays = true, true, true
	}
	if !strings.Contains(e.DateOnlyDescribe(), "Pune") {
		t.Errorf("DateOnlyDescribe() = %q, want it to name the home town", e.DateOnlyDescribe())
	}
}

func TestMergeDaysDescribeAndExample(t *testing.T) {
	e := newTestExamples(t, []string{vfs.RuleDate, vfs.RuleLocation}, false, true, false, "")
	merged := e.MergeDays()
	if strings.Count(merged, "Greece") != 1 {
		t.Errorf("MergeDays(on) example = %q, want exactly one merged Greece folder", merged)
	}

	*e.mergeDays = false
	split := e.MergeDays()
	if strings.Count(split, "Greece") != 3 {
		t.Errorf("MergeDays(off) example = %q, want three separate day-branch Greece folders", split)
	}
}

func TestIsBundleDir(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"Photos.app", true},
		{"PHOTOS.APP", true},
		{"Pictures", false},
		{"archive.zip", false},
	}
	for _, tt := range tests {
		if got := isBundleDir(tt.name); got != tt.want {
			t.Errorf("isBundleDir(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// Both answers must show the same files, so the only difference on screen is
// the folders the answer adds or leaves out.
func TestCollapseExampleShowsTheSameFilesBothWays(t *testing.T) {
	layout := []string{vfs.RuleDate, vfs.RuleDevice, vfs.RuleMedia}
	on := newTestExamples(t, layout, true, true, true, "").Collapse()
	off := newTestExamples(t, layout, false, true, true, "").Collapse()
	for _, file := range []string{"IMG_1234.jpg", "IMG_1250.jpg", "IMG_1251.jpg"} {
		if !strings.Contains(on, file) || !strings.Contains(off, file) {
			t.Errorf("%s missing from one example:\non:\n%s\noff:\n%s", file, on, off)
		}
	}
	if strings.Contains(on, "iPhone") || !strings.Contains(off, "iPhone") {
		t.Errorf("on must leave the camera folder out and off must keep it:\non:\n%s\noff:\n%s", on, off)
	}
	if strings.Contains(off, "Portrait") {
		t.Errorf("a layout without orientation folders must not show one:\n%s", off)
	}
}
