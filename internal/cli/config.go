// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configure WanderSort",
		Long: `Opens a full-screen wizard for every global setting — output folder,
folder rules, and your saved-place towns. They apply to every
scan unless overridden by a flag or environment variable, and are saved
to ~/.wandersort/config.yaml.

Prints that file to stdout instead when --print is given or when the terminal
isn't interactive (piped or redirected).`,
		Example: `# Configure
wandersort config

# Print the saved settings
wandersort config --print
wandersort config | grep rules`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runConfig(cmd)
		},
	}

	cmd.Flags().BoolP(flagPrint, "p", false, "Print the saved config file instead of opening the wizard")
	return cmd
}

func (a *app) runConfig(cmd *cobra.Command) error {
	configPath, err := a.Config.Exists()
	if err != nil {
		return fmt.Errorf("global config: %w", err)
	}

	// The wizard owns the whole terminal, so it needs one: piping or
	// redirecting means the caller wants the contents, not an alt-screen
	// fighting over the same stream.
	interactive := term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
	print, _ := cmd.Flags().GetBool(flagPrint)
	if print || !interactive {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("read config: %w", err)
		}
		fmt.Print(string(data))
		return nil
	}

	// The wizard is a tab of the one app shell, so answering the settings and
	// then scanning with them is one session rather than two invocations. The
	// shell reports the save on screen; there is no process ending here to
	// print a receipt after.
	return a.runShell(shellStart{tab: tabConfig})
}

// buildConfigForm builds the wizard's fields (seeded with the current effective
// values) and a save closure that writes them to ~/.wandersort/config.yaml.
func (a *app) buildConfigForm(ctx context.Context, geonames func() (*location.Resolver, error)) ([]*tui.Field, func() error) {
	g, _ := a.Config.Load()

	out := g.OutputPath
	if out == "" {
		out = filepath.Dir(a.Config.AppDBPath)
	}
	groupBy := append([]string{}, a.Config.Rules...)
	if len(groupBy) == 0 {
		groupBy = []string{vfs.RuleDate, vfs.RuleLocation} // sensible default
	}
	segment := "auto"
	if a.Config.SegmentMonths > 0 {
		segment = strconv.Itoa(a.Config.SegmentMonths)
	}
	collapse := a.Config.CollapseLevels
	mergeDays := a.Config.MergeSameLocationDays
	spDateOnly := a.Config.SavedPlacesDateOnly
	home, work := "", ""
	if len(g.SavedPlaces) > 0 {
		home = g.SavedPlaces[0]
	}
	if len(g.SavedPlaces) > 1 {
		work = g.SavedPlaces[1]
	}

	// Rejects a typo (close candidates exist) but waves through an unknown name
	// or a geonames database that never opened — a broken dependency can't trap this field.
	townValidator := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return nil // blank = skip
		}
		resolver, err := geonames()
		if err != nil {
			if errors.Is(err, install.ErrPending) {
				return err
			}
			return nil
		}
		_, err = resolver.Canonical(ctx, s)
		return err
	}

	// same rule at save time: the geonames spelling when it can give one,
	// else what was typed — dropping a town the user already had is data loss
	canonicalTownOrTyped := func(typed string) string {
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return ""
		}
		resolver, err := geonames()
		if err != nil {
			return typed // pending or broken geonames — never drop what was typed
		}
		if name, err := resolver.Canonical(ctx, typed); err == nil {
			return name
		}
		return "" // near-miss the validator would have rejected ("did you mean")
	}

	paths := path.New()
	homeDir := paths.HomeDir

	// Suggest locations under folders that exist on this machine — ~/Pictures
	// is a macOS/Windows convention; a Linux box without it won't offer it.
	var outSuggestions []string
	for _, c := range []string{
		filepath.Join(homeDir, "Pictures", "WanderSort"),
		filepath.Join(homeDir, "WandersortLibrary"),
	} {
		if st, err := os.Stat(filepath.Dir(c)); err == nil && st.IsDir() {
			outSuggestions = append(outSuggestions, paths.RelativeToHome(c))
		}
	}
	// suggestOut is the shared directory completion plus this field's own
	// seed suggestions for an empty input.
	suggestOut := func(typed string) []string {
		if strings.TrimSpace(typed) == "" {
			return outSuggestions
		}
		return suggestDirs(paths, typed)
	}

	suggestTown := func(typed string) []string {
		typed = strings.TrimSpace(typed)
		resolver, err := geonames()
		if len(typed) < 2 || err != nil {
			return nil
		}
		return resolver.SuggestNames(ctx, typed, 6)
	}

	rulesField := &tui.Field{
		Kind:     tui.FieldMultiSelect,
		Title:    "Rules",
		Options:  []string{vfs.RuleDate, vfs.RuleLocation, vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia},
		Selected: toMap(groupBy),
		Description: "Folder levels below Year/Month, in nesting order.\n" +
			"  date = day    location = city    device = camera\n" +
			"  orientation = portrait/landscape    media = photo/video",
	}
	ex := newConfigExamples(rulesField, &collapse, &mergeDays, &spDateOnly, &home)
	rulesField.Example = ex.Rules

	fields := []*tui.Field{
		{
			Kind:        tui.FieldInput,
			Title:       "Output path",
			Description: "Where the organized library (DB + logs) is written. ~ is fine.",
			Value:       &out,
			Placeholder: filepath.Join(homeDir, "WanderSortLibrary"),
			Suggest:     suggestOut,
		},
		rulesField,
		{
			Kind:  tui.FieldInput,
			Title: "Review segment size",
			Description: "Big libraries are reviewed in time slices, saved one at a time.\n" +
				"  auto = years when your photos span more than 3 years, else half-years.\n" +
				"  Or set the months per slice: 3, 6 or 12.",
			Value:     &segment,
			Validator: segmentValidator,
		},
		{
			Kind:        tui.FieldGroup,
			Title:       "Saved places",
			Description: "The everyday places you shoot from, and how their photos are foldered.",
			// Await blocks the form until the location database finishes downloading.
			Await: func() string {
				if _, err := geonames(); errors.Is(err, install.ErrPending) {
					return "Waiting for the location database to finish downloading…"
				}
				return ""
			},
			Subs: []*tui.Field{
				{
					Kind: tui.FieldInput, Title: "Home town", Placeholder: "e.g. Delhi (blank to skip)",
					Value: &home, Validator: townValidator, Suggest: suggestTown,
				},
				{
					Kind: tui.FieldInput, Title: "Work town", Placeholder: "blank = same as home",
					Value: &work, Validator: townValidator, Suggest: suggestTown,
				},
				{
					Kind:      tui.FieldConfirm,
					Title:     "Collapse uninformative levels?",
					Describe:  ex.CollapseDescribe,
					BoolValue: &collapse,
					Example:   ex.Collapse,
				},
				{
					Kind:      tui.FieldConfirm,
					Title:     "Group saved-place photos by date only?",
					Describe:  ex.DateOnlyDescribe,
					BoolValue: &spDateOnly,
					Example:   ex.DateOnly,
				},
				{
					Kind:      tui.FieldConfirm,
					Title:     "Merge consecutive same-location days?",
					Describe:  ex.MergeDaysDescribe,
					BoolValue: &mergeDays,
					Example:   ex.MergeDays,
				},
			},
		},
	}

	// save runs after the form completes. It reads the bound vars this form
	// wrote and persists them, replacing the whole config file.
	save := func() error {
		if strings.TrimSpace(work) == "" {
			work = home // blank work = same as home
		}
		// Collect multiselect choices from the map, in canonical option order.
		var selectedRules []string
		for _, opt := range rulesField.Options {
			if rulesField.Selected[opt] {
				selectedRules = append(selectedRules, opt)
			}
		}
		g := &config.Configuration{
			OutputPath:            paths.ExpandPath(strings.TrimSpace(out)),
			Rules:                 selectedRules,
			CollapseLevels:        collapse,
			SavedPlacesDateOnly:   spDateOnly,
			MergeSameLocationDays: mergeDays,
		}
		g.SegmentMonths, _ = strconv.Atoi(strings.TrimSpace(segment)) // "auto" → 0
		// Canonicalize towns to the exact geonames spelling before saving.
		g.SavedPlaces = []string{canonicalTownOrTyped(home), canonicalTownOrTyped(work)}
		if err := a.Config.Save(g); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
		return nil
	}
	return fields, save
}

// segmentValidator accepts the review's slice sizes: "auto", or one of the
// calendar-aligned month counts vfs.Segments buckets by. Anything else would
// silently save as 0 (auto) and look like the field did nothing.
func segmentValidator(s string) error {
	switch strings.TrimSpace(s) {
	case "auto", "3", "6", "12":
		return nil
	}
	return fmt.Errorf("must be auto, 3, 6 or 12")
}

// configExamples renders the wizard's live tree previews and their paired description text.
type configExamples struct {
	rulesField *tui.Field
	collapse   *bool
	mergeDays  *bool
	dateOnly   *bool
	home       *string
}

func newConfigExamples(rulesField *tui.Field, collapse, mergeDays, dateOnly *bool, home *string) *configExamples {
	return &configExamples{rulesField, collapse, mergeDays, dateOnly, home}
}

// exampleDay is the fixed date every wizard example uses — 2024-08 gives a
// real Year/Month pair without meaning anything beyond "a month ago".
func exampleDay(d int) time.Time { return time.Date(2024, time.August, d, 12, 0, 0, 0, time.UTC) }

// selectedRules returns every Rules option currently ticked, in canonical
// order — what the Rules field's own example demonstrates.
func (e *configExamples) selectedRules() []string {
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
func (e *configExamples) previewRules(lead ...string) []string {
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
func (e *configExamples) homeTown() string {
	if t := strings.TrimSpace(*e.home); t != "" {
		city, _, _ := strings.Cut(t, ",")
		return city
	}
	return "Indore"
}

// Rules is the Rules field's own example.
func (e *configExamples) Rules() string {
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
func (e *configExamples) Collapse() string {
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
func (e *configExamples) CollapseDescribe() string {
	d := "Drop a device/orientation/media folder that would hold every single " +
		"file in the library — one value means the level says nothing."
	if *e.collapse {
		return d + " On: those folders are left out below."
	}
	return d + " Off: they stay, repeating under every folder even though they never vary."
}

func (e *configExamples) DateOnlyDescribe() string {
	d := "Everyday shots from a saved place aren't trips — a city folder there mostly repeats itself."
	if *e.dateOnly {
		return d + " On: no city folder for these."
	}
	return d + " Off: nearby suburbs still fold into " + e.homeTown() + "."
}

func (e *configExamples) DateOnly() string {
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

func (e *configExamples) MergeDaysDescribe() string {
	d := "A multi-day trip in one place becomes one folder instead of one per day. " +
		"You can still split or merge days later in review."
	if *e.mergeDays {
		return d + " On: consecutive same-location days merge."
	}
	return d + " Off: each day keeps its own folder."
}

func (e *configExamples) MergeDays() string {
	cfg := vfs.DefaultConfig()
	cfg.Rules = e.previewRules(vfs.RuleDate, vfs.RuleLocation)
	cfg.CollapseLevels = *e.collapse
	trip := func(d int, dayOverride, file string) vfs.Sample {
		return vfs.Sample{
			TakenAt: exampleDay(d), Location: "Greece", DayOverride: dayOverride,
			Device: "iPhone 13", Width: 1170, Height: 2532,
			MediaType: classifier.MediaTypeImage, FileName: file,
		}
	}
	if *e.mergeDays {
		return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{trip(2, "02_04", "IMG_1234.jpg")})...)
	}
	// three sibling day branches under one month — exactly the
	// shape a "no" produces in review
	return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{
		trip(2, "", "IMG_1234.jpg"),
		trip(3, "", "IMG_1250.jpg"),
		trip(4, "", "IMG_1251.jpg"),
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

// maxDirSuggestions caps a completion list; a home directory with hundreds of
// folders would otherwise scroll a dropdown nobody reads.
const maxDirSuggestions = 25

// suggestDirs completes like a shell: the directories matching the typed
// prefix, written home-relative. Shared by the config wizard's output-path
// field and the shell's scan-folder input — both complete a directory, and a
// second copy of this would drift from the first.
func suggestDirs(paths *path.Resolver, typed string) []string {
	typed = paths.ExpandPath(strings.TrimSpace(typed))
	if typed == "" {
		return nil
	}
	dir, base := filepath.Split(typed)
	// Typed an existing dir → offer its children, so tab descends a level
	// at a time like shell completion.
	if st, err := os.Stat(typed); err == nil && st.IsDir() {
		dir, base = typed, ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		// bundles (.app, .framework, ...) report IsDir() true but aren't
		// folders a person would pick.
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || isBundleDir(e.Name()) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(e.Name()), strings.ToLower(base)) {
			out = append(out, paths.RelativeToHome(filepath.Join(dir, e.Name())))
			if len(out) == maxDirSuggestions {
				break
			}
		}
	}
	return out
}

// bundleExts are macOS package dirs that report IsDir() true but aren't a folder a person would pick.
// ponytail: extension list, not a bundle-detection API — add here if a report names another one.
var bundleExts = map[string]bool{".app": true}

func isBundleDir(name string) bool {
	return bundleExts[strings.ToLower(filepath.Ext(name))]
}

// toMap converts a slice to a map for multiselect field initialization.
func toMap(items []string) map[string]bool {
	m := make(map[string]bool)
	for _, item := range items {
		m[item] = true
	}
	return m
}
