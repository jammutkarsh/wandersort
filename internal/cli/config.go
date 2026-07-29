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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *app) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configure WanderSort",
		Long: `Opens a full-screen wizard for every global setting — output folder,
workers, folder rules, and your saved-place towns. They apply to every
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

	switch err := a.runConfigTUI(context.Background()); {
	case errors.Is(err, errConfigCancelled):
		fmt.Println("Cancelled — nothing saved.")
		return nil
	case err != nil:
		return err
	}
	fmt.Printf("config saved in %s\n", configPath)
	return nil
}

// errConfigCancelled signals the user quit the wizard (ctrl+c), so runConfig
// reports "nothing saved" instead of a bogus "config saved".
var errConfigCancelled = errors.New("config cancelled")

// errGazetteerPending marks the location database as still downloading. The
// town step holds on it (tui.Field.Await) while the download bar under the
// banner shows how far along it is.
var errGazetteerPending = errors.New("location database is still downloading")

// runConfigTUI runs the settings wizard as one alt-screen program. The
// location database (needed only to validate the saved-place towns) downloads in
// the background while the user answers everything above it — there is no
// install screen here; dependencies are scan's job.
func (a *app) runConfigTUI(ctx context.Context) error {
	// Console logging off for the run: the alt-screen owns the terminal and
	// the background download's log lines would draw over it. The file log
	// still captures everything.
	a.Log = logger.NewTUI(a.Config.LogLevel, a.Config.LogFile, func(logger.Event) {})

	// gazetteer is a non-blocking peek at the location resolver: errGazetteerPending
	// while still downloading, otherwise whatever a.Deps.Location() resolved to
	// (never blocks, since LocationReady() already gates it) — this is the one
	// place the form reads the resolver, no field on app to race against.
	var coord *install.Coordinator
	gazetteer := func() (*location.Resolver, error) {
		if !coord.LocationReady() {
			return nil, errGazetteerPending
		}
		return coord.Location()
	}

	fields, save := a.buildConfigForm(ctx, gazetteer)
	prog := tea.NewProgram(tui.NewFormModel(fields, save), tea.WithAltScreen(), tea.WithOutput(os.Stderr))

	// Download the location database while the form is answered, reporting into
	// the form's own progress row (tui.DownloadMsg) — no install screen, and
	// nothing at all on screen when it's already on disk, since a no-op install
	// reports no bytes and only ever sends the Finished message.
	coord = a.newDeps(func(_ string, done, total int64) {
		prog.Send(tui.DownloadMsg{Label: "Location database", Done: done, Total: total})
	})
	a.Deps = coord
	coord.StartLocationOnly(ctx, func(error) {
		prog.Send(tui.DownloadMsg{Finished: true})
	})

	final, err := prog.Run()
	if err != nil {
		return fmt.Errorf("config ui: %w", err)
	}
	if fm, ok := final.(tui.FormModel); ok {
		if fm.IsAborted() {
			return errConfigCancelled
		}
		return fm.Error()
	}
	return nil
}

// buildConfigForm builds the wizard's fields (seeded with the current effective
// values) and a save closure that writes them to ~/.wandersort/config.yaml.
func (a *app) buildConfigForm(ctx context.Context, gazetteer func() (*location.Resolver, error)) ([]*tui.Field, func() error) {
	g, _ := a.Config.Load()

	out := g.OutputPath
	if out == "" {
		out = filepath.Dir(a.Config.AppDBPath)
	}
	workers := strconv.Itoa(a.Config.Workers)
	groupBy := append([]string{}, a.Config.Rules...)
	if len(groupBy) == 0 {
		groupBy = []string{vfs.RuleDate, vfs.RuleLocation} // sensible default
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

	// townValidator rejects a near-miss the gazetteer has close candidates for
	// (a typo), but waves through a name it doesn't know at all — see
	// canonicalTown. A gazetteer that never opened at all waves the town
	// through too — a broken dependency must not trap the user on this field.
	townValidator := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return nil // blank = skip
		}
		resolver, err := gazetteer()
		if err != nil {
			if errors.Is(err, errGazetteerPending) {
				return err
			}
			return nil
		}
		if _, err := canonicalTown(ctx, resolver, s); err != nil {
			return err
		}
		return nil
	}

	// same rule at save time: the gazetteer's spelling when it can give one,
	// else what was typed — dropping a town the user already had is data loss
	canonicalTownOrTyped := func(typed string) string {
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return ""
		}
		resolver, err := gazetteer()
		if err != nil {
			return typed // pending or broken gazetteer — never drop what was typed
		}
		if name, err := canonicalTown(ctx, resolver, typed); err == nil {
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
		filepath.Join(homeDir, "WanderSortLibrary"),
	} {
		if st, err := os.Stat(filepath.Dir(c)); err == nil && st.IsDir() {
			outSuggestions = append(outSuggestions, paths.RelativeToHome(c))
		}
	}
	// suggestOut completes like a shell: list directories matching the typed
	// prefix. Blank input falls back to the canned platform locations. Results
	// render home-relative (~/Pictures, not /Users/x/Pictures) — ExpandPath
	// undoes it before the value is used for anything. Capped well past the
	// 5-row form window (tui.maxFormSuggestions) — the form scrolls the list,
	// so a directory with a dozen matching children shouldn't be pruned before
	// the user ever gets a chance to scroll to it.
	const maxOutSuggestions = 25
	suggestOut := func(typed string) []string {
		typed = paths.ExpandPath(strings.TrimSpace(typed))
		if typed == "" {
			return outSuggestions
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
			// folders a person would pick as an output path.
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || isBundleDir(e.Name()) {
				continue
			}
			if strings.HasPrefix(strings.ToLower(e.Name()), strings.ToLower(base)) {
				out = append(out, paths.RelativeToHome(filepath.Join(dir, e.Name())))
				if len(out) == maxOutSuggestions {
					break
				}
			}
		}
		return out
	}

	// suggestTown live-searches the gazetteer as the user types, so saved-place
	// only ever get names the location DB can resolve later. Silent while the
	// database is still downloading.
	suggestTown := func(typed string) []string {
		typed = strings.TrimSpace(typed)
		resolver, err := gazetteer()
		if len(typed) < 2 || err != nil {
			return nil
		}
		matches, err := resolver.SearchByName(ctx, typed, 6)
		if err != nil {
			return nil
		}
		// FullName, not the bare city: six identical "Springfield"s are
		// unpickable. The saved town keeps that form; ResolveByName round-trips it.
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.FullName
		}
		return names
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
	// exampleDay is the fixed date every wizard example uses — 2024-08 gives a
	// real Year/Month pair without meaning anything beyond "a month ago".
	exampleDay := func(d int) time.Time { return time.Date(2024, time.August, d, 12, 0, 0, 0, time.UTC) }
	// selectedRules returns every Rules option currently ticked, in canonical
	// order — what the Rules field's own example demonstrates.
	selectedRules := func() []string {
		var out []string
		for _, r := range rulesField.Options {
			if rulesField.Selected[r] {
				out = append(out, r)
			}
		}
		return out
	}
	// previewRules is lead (e.g. Date, Location — always shown regardless of
	// whether the user ticked them) plus the trailing collapsible levels the
	// user *has* ticked — every example below the Rules field itself wants to
	// demonstrate its own question regardless of what Rules holds.
	previewRules := func(lead ...string) []string {
		rules := append([]string{}, lead...)
		for _, r := range []string{vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia} {
			if rulesField.Selected[r] {
				rules = append(rules, r)
			}
		}
		return rules
	}
	// homeTown is what the saved-place examples name; a stand-in until the user
	// has typed a town, so the example is never blank. `home` is saved fully
	// qualified ("Indore, Madhya Pradesh, India") so it round-trips through
	// ResolveByName, but a folder can't hold a comma and the state/country
	// only exists to disambiguate the picker — bare city here, same as the
	// anchor fold in vfs.resolveLocations.
	homeTown := func() string {
		if t := strings.TrimSpace(home); t != "" {
			city, _, _ := strings.Cut(t, ",")
			return city
		}
		return "Indore"
	}
	rulesField.Example = func() string {
		cfg := vfs.DefaultConfig()
		cfg.Rules = selectedRules()
		cfg.CollapseLevels = false // always show every level, to demonstrate the order itself
		sample := vfs.Sample{
			TakenAt: exampleDay(2), Location: "Goa", Device: "iPhone 13",
			Width: 1170, Height: 2532, MediaType: classifier.MediaTypeImage, FileName: "IMG_1234.jpg",
		}
		return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{sample})...)
	}

	// Always demonstrates all three collapsible levels even when Rules has none
	// ticked: this step sits under Saved places, so the user may not have
	// visited Rules yet, and "nothing to collapse" teaches them nothing.
	// Two branches, not one — collapsing is about the *repetition* of a
	// one-value level under every folder, which a single path can't show.
	collapseExample := func() string {
		cfg := vfs.DefaultConfig()
		var lead []string
		if rulesField.Selected[vfs.RuleDate] {
			lead = append(lead, vfs.RuleDate)
		}
		if rulesField.Selected[vfs.RuleLocation] {
			lead = append(lead, vfs.RuleLocation)
		}
		cfg.Rules = append(lead, vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia)
		cfg.CollapseLevels = collapse

		day := func(d int) vfs.Sample {
			return vfs.Sample{
				TakenAt: exampleDay(d), Location: homeTown(), Device: "iPhone 13",
				Width: 1170, Height: 2532, MediaType: classifier.MediaTypeImage,
			}
		}
		day12, day13a, day13b := day(12), day(13), day(13)
		day12.FileName, day13a.FileName, day13b.FileName = "IMG_1234.jpg", "IMG_1250.jpg", "IMG_1251.jpg"
		if !collapse {
			// Second branch uses a different device and orientation than the
			// first — real, so it also shows what does *not* collapse: two
			// devices and two orientations mean neither level is one value
			// library-wide, only Photos is.
			day13a.Device, day13a.Width, day13a.Height = "Canon EOS 700D", 6000, 4000
			day13b.Device, day13b.Width, day13b.Height = "Canon EOS 700D", 6000, 4000
		}
		return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{day12, day13a, day13b})...)
	}
	// What the example *means*, as the question's own description — the example
	// column is too narrow to hold a sentence without truncating it.
	collapseDescribe := func() string {
		d := "Drop a device/orientation/media folder that would hold every single " +
			"file in the library — one value means the level says nothing."
		if collapse {
			return d + " On: those folders are left out below."
		}
		return d + " Off: they stay, repeating under every folder even though they never vary."
	}

	fields := []*tui.Field{
		{
			Kind:        tui.FieldInput,
			Title:       "Output path",
			Description: "Where the organized library (DB + logs) is written. ~ is fine.",
			Value:       &out,
			Placeholder: filepath.Join(homeDir, "WanderSortLibrary"),
			Suggest:     suggestOut,
		},
		{
			Kind:        tui.FieldInput,
			Title:       "Worker count",
			Description: "Parallel hashing/EXIF workers.",
			Value:       &workers,
			Validator: func(s string) error {
				if _, err := strconv.Atoi(strings.TrimSpace(s)); err != nil {
					return fmt.Errorf("must be a number")
				}
				return nil
			},
		},
		rulesField,
		{
			Kind:        tui.FieldGroup,
			Title:       "Saved places",
			Description: "The everyday places you shoot from, and how their photos are foldered.",
			// The town fields need the gazetteer, so this is the one step that
			// waits for the background download. Everything above it is
			// answerable meanwhile, which is the point of not having an install
			// screen.
			Await: func() string {
				if _, err := gazetteer(); errors.Is(err, errGazetteerPending) {
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
					Describe:  collapseDescribe,
					BoolValue: &collapse,
					Example:   collapseExample,
				},
				{
					Kind:  tui.FieldConfirm,
					Title: "Group saved-place photos by date only?",
					Describe: func() string {
						d := "Everyday shots from a saved place aren't trips — a city folder there mostly repeats itself."
						if spDateOnly {
							return d + " On: no city folder for these."
						}
						return d + " Off: nearby suburbs still fold into " + homeTown() + "."
					},
					BoolValue: &spDateOnly,
					Example: func() string {
						cfg := vfs.DefaultConfig()
						cfg.Rules = previewRules(vfs.RuleDate, vfs.RuleLocation)
						cfg.CollapseLevels = collapse
						cfg.SavedPlacesDateOnly = spDateOnly
						sample := vfs.Sample{
							TakenAt: exampleDay(12), Location: homeTown(), AtSavedPlace: true, Device: "iPhone 13",
							Width: 1170, Height: 2532, MediaType: classifier.MediaTypeImage, FileName: "IMG_1234.jpg",
						}
						return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{sample})...)
					},
				},
				{
					Kind:  tui.FieldConfirm,
					Title: "Merge consecutive same-location days?",
					Describe: func() string {
						d := "A multi-day trip in one place becomes one folder instead of one per day. " +
							"You can still split or merge days later in review."
						if mergeDays {
							return d + " On: consecutive same-location days merge."
						}
						return d + " Off: each day keeps its own folder."
					},
					BoolValue: &mergeDays,
					Example: func() string {
						cfg := vfs.DefaultConfig()
						cfg.Rules = previewRules(vfs.RuleDate, vfs.RuleLocation)
						cfg.CollapseLevels = collapse
						trip := func(d int, dayOverride, file string) vfs.Sample {
							return vfs.Sample{
								TakenAt: exampleDay(d), Location: "Greece", DayOverride: dayOverride,
								Device: "iPhone 13", Width: 1170, Height: 2532,
								MediaType: classifier.MediaTypeImage, FileName: file,
							}
						}
						if mergeDays {
							return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{trip(2, "02_04", "IMG_1234.jpg")})...)
						}
						// three sibling day branches under one month — exactly the
						// shape a "no" produces in review
						return treeExample("", vfs.PreviewPaths(cfg, []vfs.Sample{
							trip(2, "", "IMG_1234.jpg"),
							trip(3, "", "IMG_1250.jpg"),
							trip(4, "", "IMG_1251.jpg"),
						})...)
					},
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
		if w, err := strconv.Atoi(strings.TrimSpace(workers)); err == nil {
			g.Workers = w
		}
		// Canonicalize towns to the exact gazetteer spelling before saving.
		g.SavedPlaces = []string{canonicalTownOrTyped(home), canonicalTownOrTyped(work)}
		if err := a.Config.Save(g); err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
		return nil
	}
	return fields, save
}

// treeExample renders slash paths as the same guided tree the review screen
// draws, shared prefixes folded together — a wizard example should look like
// the thing the setting shapes, not like a shell path. note, if any, renders
// as its own final line — appended to the deepest tree line it was the first
// thing the side panel truncated.
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

// bundleExts are macOS package directories that report IsDir() true but hold
// an app/plugin, not something a person would pick as an output folder.
// ponytail: extension list, not a bundle-detection API — add here if a report
// names another one (.framework, .kext, ...).
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

// canonicalTown returns the gazetteer's exact spelling of a typed town name.
// A name the gazetteer has never heard of is accepted as typed — a village
// missing from the database is the user's problem to spell, not a wall — but a
// near-miss with real candidates still errors ("did you mean"), since that's a
// typo, not a gap. Blank input, or a nil resolver (not ready yet), is an error
// (callers treat both as "skip" before calling for the canonical form).
// Takes the resolver explicitly rather than reading a shared field — the
// caller already knows it's ready (see the gazetteer closures in buildConfigForm).
func canonicalTown(ctx context.Context, resolver *location.Resolver, typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" || resolver == nil {
		return "", fmt.Errorf("no town")
	}
	matches, err := resolver.SearchByName(ctx, typed, 8)
	if err != nil || len(matches) == 0 {
		return typed, nil
	}
	if name, ok := exactMatch(matches, typed); ok {
		return name, nil
	}
	return "", fmt.Errorf("no exact match for %q (did you mean %s?)", typed, matches[0].Name)
}

// exactMatch returns the gazetteer's own spelling when one of matches is a
// case-insensitive match for typed, e.g. so a user who already typed the
// right name gets the canonical form without extra steps (see canonicalTown).
// The town picker lists full names ("Indore, Madhya Pradesh, India"), so all
// three forms match — and whichever matched is saved as the full name, the
// only form that names one row for certain (see location.ResolveByName).
func exactMatch(matches []location.PlaceMatch, typed string) (string, bool) {
	typed = strings.TrimSpace(typed)
	for _, m := range matches {
		if strings.EqualFold(m.FullName, typed) {
			return canonicalNameOf(m), true
		}
	}
	for _, m := range matches {
		if strings.EqualFold(m.DisplayName, typed) || strings.EqualFold(m.Name, typed) {
			return canonicalNameOf(m), true
		}
	}
	return "", false
}

// canonicalNameOf is the form an anchor is saved as: the full name when the
// gazetteer gave one, else whatever shorter form it has.
func canonicalNameOf(m location.PlaceMatch) string {
	switch {
	case m.FullName != "":
		return m.FullName
	case m.DisplayName != "":
		return m.DisplayName
	default:
		return m.Name
	}
}
