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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (a *App) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configure WanderSort",
		Long: `Opens a full-screen wizard for every global setting — output folder,
workers, folder rules, and your home/work towns. They apply to every
scan/serve unless overridden by a flag or environment variable, and are saved
to ~/.wandersort/config.yaml.

Prints that file to stdout instead when --print is given or when the terminal
isn't interactive (piped or redirected).`,
		Example: `# Configure
wandersort config

# Print the saved settings
wandersort config --print
wandersort config | grep rules`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runConfig()
		},
	}

	cmd.Flags().BoolP(flagPrint, "p", false, "Print the saved config file instead of opening the wizard")
	return cmd
}

func (a *App) runConfig() error {
	configPath, err := config.EnsureGlobalConfigFile()
	if err != nil {
		return fmt.Errorf("global config: %w", err)
	}

	// The wizard owns the whole terminal, so it needs one: piping or
	// redirecting means the caller wants the contents, not an alt-screen
	// fighting over the same stream.
	interactive := term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
	if v.GetBool(flagPrint) || !interactive {
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
// location database (needed only to validate the home/work towns) downloads in
// the background while the user answers everything above it — there is no
// install screen here; dependencies are scan's job.
func (a *App) runConfigTUI(ctx context.Context) error {
	// Console logging off for the run: the alt-screen owns the terminal and
	// the background download's log lines would draw over it. The file log
	// still captures everything.
	a.Log = logger.NewTUI(a.Config.LogLevel, a.Config.LogFile, func(logger.Event) {})

	var locErr error
	ready := make(chan struct{})
	// gazetteer reports whether the town fields can be used yet. Reads of
	// a.LocationResolver only happen once ready is closed, which is what makes
	// them safe from the download goroutine below.
	gazetteer := func() error {
		select {
		case <-ready:
			return locErr
		default:
			return errGazetteerPending
		}
	}

	fields, save := a.buildConfigForm(ctx, gazetteer)
	prog := tea.NewProgram(tui.NewFormModel(fields, save), tea.WithAltScreen(), tea.WithOutput(os.Stderr))

	// Download the location database while the form is answered, reporting into
	// the form's own progress row (tui.DownloadMsg) — no install screen, and
	// nothing at all on screen when it's already on disk, since a no-op install
	// reports no bytes and only ever sends the Finished message.
	a.InstallProgress = func(_ string, done, total int64) {
		prog.Send(tui.DownloadMsg{Label: "Location database", Done: done, Total: total})
	}
	go func() {
		locErr = a.InitLocationResolver(ctx)
		close(ready)
		prog.Send(tui.DownloadMsg{Finished: true})
	}()

	final, err := prog.Run()
	a.InstallProgress = nil
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
func (a *App) buildConfigForm(ctx context.Context, gazetteer func() error) ([]*tui.Field, func() error) {
	g, _ := config.LoadGlobal()

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
	hwDateOnly := a.Config.HomeWorkDateOnly
	home := g.HomeWork.Home
	work := g.HomeWork.Work

	// townValidator rejects a near-miss the gazetteer has close candidates for
	// (a typo), but waves through a name it doesn't know at all — see
	// canonicalTown. A gazetteer that never opened at all waves the town
	// through too — a broken dependency must not trap the user on this field.
	townValidator := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return nil // blank = skip
		}
		if err := gazetteer(); err != nil {
			if errors.Is(err, errGazetteerPending) {
				return err
			}
			return nil
		}
		if _, err := a.canonicalTown(ctx, s); err != nil {
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
		if name, err := a.canonicalTown(ctx, typed); err == nil {
			return name
		}
		if gazetteer() != nil {
			return typed
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

	// suggestTown live-searches the gazetteer as the user types, so home/work
	// only ever get names the location DB can resolve later. Silent while the
	// database is still downloading.
	suggestTown := func(typed string) []string {
		typed = strings.TrimSpace(typed)
		if len(typed) < 2 || gazetteer() != nil {
			return nil
		}
		matches, err := a.LocationResolver.SearchByName(ctx, typed, 6)
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
	ruleSeg := map[string]string{
		vfs.RuleDevice:      "iPhone 13",
		vfs.RuleOrientation: "Vertical",
		vfs.RuleMedia:       "Photos",
	}
	// examplePath builds the path the answers *so far* would produce, so an
	// example reacts to the rules ticked earlier rather than showing a canned
	// tree. Blank day/town drop that level; collapsed drops the other three.
	// Rendered through treeExample so the example looks like the review screen
	// — the thing these settings actually shape.
	examplePath := func(day, town string, collapsed bool) string {
		segs := []string{"2024", "08_August"}
		for _, r := range rulesField.Options {
			if !rulesField.Selected[r] {
				continue
			}
			switch r {
			case vfs.RuleDate:
				if day != "" {
					segs = append(segs, day)
				}
			case vfs.RuleLocation:
				if town != "" {
					segs = append(segs, town)
				}
			default:
				if !collapsed {
					segs = append(segs, ruleSeg[r])
				}
			}
		}
		return strings.Join(segs, "/") + "/IMG_1234.jpg"
	}
	// hwExample builds the path for the home/work date-only example. Unlike
	// examplePath, the location segment is driven directly by loc rather than
	// rulesField.Selected — this question is specifically about the home/work
	// city folder, so the example must show its effect even when Rules hasn't
	// got location ticked yet.
	hwExample := func(loc string) string {
		segs := []string{"2024", "08_August"}
		if rulesField.Selected[vfs.RuleDate] {
			segs = append(segs, "12")
		}
		if loc != "" {
			segs = append(segs, loc)
		}
		if !collapse {
			for _, r := range []string{vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia} {
				if rulesField.Selected[r] {
					segs = append(segs, ruleSeg[r])
				}
			}
		}
		return strings.Join(segs, "/") + "/IMG_1234.jpg"
	}
	// homeTown is what the home/work examples name; a stand-in until the user
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
	rulesField.Example = func() string { return treeExample("", examplePath("02", "Goa", false)) }

	// Always demonstrates all three collapsible levels even when Rules has none
	// ticked: this step sits under Home & work, so the user may not have
	// visited Rules yet, and "nothing to collapse" teaches them nothing.
	// Two branches, not one — collapsing is about the *repetition* of a
	// one-value level under every folder, which a single path can't show.
	collapseExample := func() string {
		day := func(d string) []string {
			segs := []string{"2024", "08_August"}
			if rulesField.Selected[vfs.RuleDate] {
				segs = append(segs, d)
			}
			if rulesField.Selected[vfs.RuleLocation] {
				segs = append(segs, homeTown())
			}
			return segs
		}
		leaf := func(segs []string, device, orientation, file string) string {
			out := append([]string{}, segs...)
			if !collapse {
				out = append(out, device, orientation, ruleSeg[vfs.RuleMedia])
			}
			return strings.Join(out, "/") + "/" + file
		}
		if collapse {
			d13 := day("13")
			return treeExample("",
				leaf(day("12"), "", "", "IMG_1234.jpg"),
				leaf(d13, "", "", "IMG_1250.jpg"),
				leaf(d13, "", "", "IMG_1251.jpg"))
		}
		// Second branch uses a different device and orientation than the
		// first — real, so it also shows what does *not* collapse: two
		// devices and two orientations mean neither level is one value
		// library-wide, only Photos is.
		d13 := day("13")
		return treeExample("",
			leaf(day("12"), ruleSeg[vfs.RuleDevice], ruleSeg[vfs.RuleOrientation], "IMG_1234.jpg"),
			leaf(d13, "Canon EOS 700D", "Horizontal", "IMG_1250.jpg"),
			leaf(d13, "Canon EOS 700D", "Horizontal", "IMG_1251.jpg"))
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
			Title:       "Home & work",
			Description: "The everyday places you shoot from, and how their photos are foldered.",
			// The town fields need the gazetteer, so this is the one step that
			// waits for the background download. Everything above it is
			// answerable meanwhile, which is the point of not having an install
			// screen.
			Await: func() string {
				if errors.Is(gazetteer(), errGazetteerPending) {
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
					Title: "Group home/work photos by date only?",
					Describe: func() string {
						d := "Everyday shots from home/work aren't trips — a city folder there mostly repeats itself."
						if hwDateOnly {
							return d + " On: no city folder for these."
						}
						return d + " Off: nearby suburbs still fold into " + homeTown() + "."
					},
					BoolValue: &hwDateOnly,
					Example: func() string {
						if hwDateOnly {
							return treeExample("", hwExample(""))
						}
						return treeExample("", hwExample(homeTown()))
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
						if mergeDays {
							return treeExample("", examplePath("02_04", "Greece", collapse))
						}
						// three sibling day branches under one month — exactly the
						// shape a "no" produces in review
						return treeExample("",
							examplePath("02", "Greece", collapse),
							examplePath("03", "Greece", collapse),
							examplePath("04", "Greece", collapse))
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
		g := config.Global{
			OutputPath:            paths.ExpandPath(strings.TrimSpace(out)),
			Rules:                 selectedRules,
			CollapseLevels:        collapse,
			HomeWorkDateOnly:      hwDateOnly,
			MergeSameLocationDays: mergeDays,
		}
		if w, err := strconv.Atoi(strings.TrimSpace(workers)); err == nil {
			g.Workers = w
		}
		// Canonicalize towns to the exact gazetteer spelling before saving.
		g.HomeWork.Home = canonicalTownOrTyped(home)
		g.HomeWork.Work = canonicalTownOrTyped(work)
		if err := config.SaveGlobal(g); err != nil {
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
	var b strings.Builder
	var walk func(nodes []*node, prefix string, top bool)
	walk = func(nodes []*node, prefix string, top bool) {
		for i, n := range nodes {
			branch, childPrefix := "├─ ", prefix+"│  "
			if i == len(nodes)-1 {
				branch, childPrefix = "└─ ", prefix+"   "
			}
			if top { // roots stand alone, same as the review tree
				branch, childPrefix = "", ""
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(prefix)
			b.WriteString(branch)
			b.WriteString(n.name)
			walk(n.kids, childPrefix, false)
		}
	}
	walk(root.kids, "", true)
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
// typo, not a gap. Blank input is an error (callers treat blank as "skip"
// before calling for the canonical form).
func (a *App) canonicalTown(ctx context.Context, typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" || a.LocationResolver == nil {
		return "", fmt.Errorf("no town")
	}
	matches, err := a.LocationResolver.SearchByName(ctx, typed, 8)
	if err != nil || len(matches) == 0 {
		return typed, nil
	}
	if name, ok := exactMatch(matches, typed); ok {
		return name, nil
	}
	return "", fmt.Errorf("no exact match for %q (did you mean %s?)", typed, matches[0].Name)
}
