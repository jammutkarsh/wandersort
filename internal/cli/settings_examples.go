// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"strings"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// settingsExamples renders the wizard's live tree previews and their paired description text.
type settingsExamples struct {
	rulesField *tui.Field
	collapse   *bool
	mergeDays  *bool
	dateOnly   *bool
	home       *string
}

func newSettingsExamples(rulesField *tui.Field, collapse, mergeDays, dateOnly *bool, home *string) *settingsExamples {
	return &settingsExamples{rulesField, collapse, mergeDays, dateOnly, home}
}

// exampleDay is the fixed date every wizard example uses — 2024-08 gives a
// real Year/Month pair without meaning anything beyond "a month ago".
func exampleDay(d int) time.Time { return time.Date(2024, time.August, d, 12, 0, 0, 0, time.UTC) }

// selectedRules returns every Rules option currently ticked, in canonical
// order — what the Rules field's own example demonstrates.
func (e *settingsExamples) selectedRules() []string {
	var out []string
	for _, r := range e.rulesField.Options {
		if e.rulesField.Selected[r] {
			out = append(out, r)
		}
	}
	return out
}

// previewRules is lead (always shown) plus the collapsible levels the user
// has ticked — each example demonstrates its own question, not the live Rules value.
func (e *settingsExamples) previewRules(lead ...string) []string {
	rules := append([]string{}, lead...)
	for _, r := range []string{vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia} {
		if e.rulesField.Selected[r] {
			rules = append(rules, r)
		}
	}
	return rules
}

// homeTown is what the saved-place examples name; a stand-in until the user
// has typed a town, so the example is never blank.
func (e *settingsExamples) homeTown() string {
	if t := strings.TrimSpace(*e.home); t != "" {
		city, _, _ := strings.Cut(t, ",")
		return city
	}
	return "Indore"
}

// Rules is the Rules field's own example.
func (e *settingsExamples) Rules() string {
	cfg := vfs.DefaultConfig()
	cfg.Rules = e.selectedRules()
	cfg.CollapseLevels = false // always show every level, to demonstrate the order itself
	sample := vfs.Sample{
		TakenAt: exampleDay(2), Location: "Goa", Device: "iPhone 13",
		Width: 1170, Height: 2532, MediaType: classifier.MediaTypeImage, FileName: "IMG_1234.jpg",
	}
	return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{sample})...)
}

// Collapse always demonstrates all three collapsible levels even when Rules has none ticked
func (e *settingsExamples) Collapse() string {
	cfg := vfs.DefaultConfig()
	var lead []string
	if e.rulesField.Selected[vfs.RuleDate] {
		lead = append(lead, vfs.RuleDate)
	}
	if e.rulesField.Selected[vfs.RuleLocation] {
		lead = append(lead, vfs.RuleLocation)
	}
	cfg.Rules = append(lead, vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia)
	cfg.CollapseLevels = *e.collapse

	day := func(d int) vfs.Sample {
		return vfs.Sample{
			TakenAt: exampleDay(d), Location: e.homeTown(), Device: "iPhone 13",
			Width: 1170, Height: 2532, MediaType: classifier.MediaTypeImage,
		}
	}
	day12, day13a, day13b := day(12), day(13), day(13)
	day12.FileName, day13a.FileName, day13b.FileName = "IMG_1234.jpg", "IMG_1250.jpg", "IMG_1251.jpg"
	if !*e.collapse {
		// Second branch differs in device/orientation, showing what does NOT
		// collapse: neither level is one value library-wide, only Photos is.
		day13a.Device, day13a.Width, day13a.Height = "Canon EOS 700D", 6000, 4000
		day13b.Device, day13b.Width, day13b.Height = "Canon EOS 700D", 6000, 4000
	}
	return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{day12, day13a, day13b})...)
}

// CollapseDescribe is what Collapse *means*, as the question's own
// description — the example column is too narrow to hold a sentence without
// truncating it.
func (e *settingsExamples) CollapseDescribe() string {
	d := "Drop a device/orientation/media folder that would hold every single " +
		"file in the library — one value means the level says nothing."
	if *e.collapse {
		return d + " On: those folders are left out below."
	}
	return d + " Off: they stay, repeating under every folder even though they never vary."
}

func (e *settingsExamples) DateOnlyDescribe() string {
	d := "Everyday shots from a saved place aren't trips — a city folder there mostly repeats itself."
	if *e.dateOnly {
		return d + " On: no city folder for these."
	}
	return d + " Off: nearby suburbs still fold into " + e.homeTown() + "."
}

func (e *settingsExamples) DateOnly() string {
	cfg := vfs.DefaultConfig()
	cfg.Rules = e.previewRules(vfs.RuleDate, vfs.RuleLocation)
	cfg.CollapseLevels = *e.collapse
	cfg.SavedPlacesDateOnly = *e.dateOnly
	sample := vfs.Sample{
		TakenAt: exampleDay(12), Location: e.homeTown(), AtSavedPlace: true, Device: "iPhone 13",
		Width: 1170, Height: 2532, MediaType: classifier.MediaTypeImage, FileName: "IMG_1234.jpg",
	}
	return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{sample})...)
}

func (e *settingsExamples) MergeDaysDescribe() string {
	d := "A multi-day trip in one place becomes one folder instead of one per day. " +
		"You can still split or merge days later in review."
	if *e.mergeDays {
		return d + " On: consecutive same-location days merge."
	}
	return d + " Off: each day keeps its own folder."
}

func (e *settingsExamples) MergeDays() string {
	cfg := vfs.DefaultConfig()
	cfg.Rules = e.previewRules(vfs.RuleDate, vfs.RuleLocation)
	cfg.CollapseLevels = *e.collapse
	cfg.MergeSameLocationDays = *e.mergeDays
	trip := func(d int, file string) vfs.Sample {
		return vfs.Sample{
			TakenAt: exampleDay(d), Location: "Greece",
			Device: "iPhone 13", Width: 1170, Height: 2532,
			MediaType: classifier.MediaTypeImage, FileName: file,
		}
	}
	// The same three days either way — the setting itself decides whether
	// they fold into one range or stay as three sibling branches under one
	// month. The "yes" answer used to be a hand-written "02_04" handed
	// straight to the renderer, which is not what the merge produces so much
	// as a claim about it.
	return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{
		trip(2, "IMG_1234.jpg"),
		trip(3, "IMG_1250.jpg"),
		trip(4, "IMG_1251.jpg"),
	})...)
}

// treeExample renders slash paths as the same guided tree the review screen
// draws, shared prefixes folded together
func treeExample(note string, paths ...string) string {
	type node struct {
		name string
		kids []*node
	}
	root := &node{}
	for _, p := range paths {
		cur := root
		for seg := range strings.SplitSeq(p, "/") {
			var next *node
			for _, k := range cur.kids {
				if k.name == seg {
					next = k
					break
				}
			}
			if next == nil {
				next = &node{name: seg}
				cur.kids = append(cur.kids, next)
			}
			cur = next
		}
	}
	var names []string
	var depths []int
	var walk func(nodes []*node, depth int)
	walk = func(nodes []*node, depth int) {
		for _, n := range nodes {
			names = append(names, n.name)
			depths = append(depths, depth)
			walk(n.kids, depth+1)
		}
	}
	walk(root.kids, 0)

	guides := tui.Guides(depths)
	var b strings.Builder
	for i, name := range names {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(guides[i])
		b.WriteString(name)
	}
	if note != "" {
		b.WriteString("\n\n")
		b.WriteString(note)
	}
	return b.String()
}
