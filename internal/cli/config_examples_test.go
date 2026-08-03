// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func newTestExamples(t *testing.T, selected []string, collapse, mergeDays, dateOnly bool, home string) *configExamples {
	t.Helper()
	rulesField := &tui.Field{
		Options:  []string{vfs.RuleDate, vfs.RuleLocation, vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia},
		Selected: toMap(selected),
	}
	return newConfigExamples(rulesField, &collapse, &mergeDays, &dateOnly, &home)
}

func TestSelectedRules(t *testing.T) {
	e := newTestExamples(t, []string{vfs.RuleDate, vfs.RuleDevice}, false, false, false, "")
	got := e.selectedRules()
	want := []string{vfs.RuleDate, vfs.RuleDevice}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("selectedRules() = %v, want %v (in canonical Options order)", got, want)
	}
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

func TestCollapseDescribe(t *testing.T) {
	e := newTestExamples(t, nil, true, false, false, "")
	on := e.CollapseDescribe()
	if !strings.Contains(on, "On:") {
		t.Errorf("CollapseDescribe(on) = %q, want it to say On:", on)
	}
	*e.collapse = false
	off := e.CollapseDescribe()
	if !strings.Contains(off, "Off:") {
		t.Errorf("CollapseDescribe(off) = %q, want it to say Off:", off)
	}
}

func TestDateOnlyDescribe(t *testing.T) {
	e := newTestExamples(t, nil, false, false, true, "Pune")
	on := e.DateOnlyDescribe()
	if !strings.Contains(on, "On:") {
		t.Errorf("DateOnlyDescribe(on) = %q, want it to say On:", on)
	}
	*e.dateOnly = false
	off := e.DateOnlyDescribe()
	if !strings.Contains(off, "Pune") {
		t.Errorf("DateOnlyDescribe(off) = %q, want it to name the home town", off)
	}
}

func TestMergeDaysDescribeAndExample(t *testing.T) {
	e := newTestExamples(t, []string{vfs.RuleDate, vfs.RuleLocation}, false, true, false, "")
	on := e.MergeDaysDescribe()
	if !strings.Contains(on, "On:") {
		t.Errorf("MergeDaysDescribe(on) = %q, want it to say On:", on)
	}
	merged := e.MergeDays()
	if strings.Count(merged, "Greece") != 1 {
		t.Errorf("MergeDays(on) example = %q, want exactly one merged Greece folder", merged)
	}

	*e.mergeDays = false
	off := e.MergeDaysDescribe()
	if !strings.Contains(off, "Off:") {
		t.Errorf("MergeDaysDescribe(off) = %q, want it to say Off:", off)
	}
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

func TestToMap(t *testing.T) {
	got := toMap([]string{"a", "b", "a"})
	if len(got) != 2 || !got["a"] || !got["b"] {
		t.Errorf("toMap([a b a]) = %v, want set {a, b}", got)
	}
	if empty := toMap(nil); len(empty) != 0 {
		t.Errorf("toMap(nil) = %v, want empty map", empty)
	}
}
