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
	path, err := config.EnsureGlobalConfigFile()
	if err != nil {
		return fmt.Errorf("global config: %w", err)
	}

	// The wizard owns the whole terminal, so it needs one: piping or
	// redirecting means the caller wants the contents, not an alt-screen
	// fighting over the same stream.
	interactive := term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
	if v.GetBool(flagPrint) || !interactive {
		data, err := os.ReadFile(path)
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
	fmt.Printf("config saved in %s\n", path)
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

	// townValidator rejects a town the gazetteer doesn't know, so the saved
	// place always resolves later. A gazetteer that never opened at all (failed
	// download, database held by another process) waves the town through
	// instead: a broken dependency must not trap the user on this field with no
	// way forward.
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

	// canonicalTownOrTyped is the same rule at save time: the gazetteer's exact
	// spelling when it can give one, otherwise what the user typed — dropping a
	// town they already had because the database wouldn't open would be a silent
	// data loss.
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
		return "" // gazetteer works and doesn't know this town
	}

	homeDir, _ := os.UserHomeDir()

	// expandHome resolves a leading ~ so "~/Photos" works everywhere the path
	// is matched or saved.
	expandHome := func(p string) string {
		if p == "~" {
			return homeDir
		}
		if strings.HasPrefix(p, "~/") {
			return filepath.Join(homeDir, p[2:])
		}
		return p
	}

	// Suggest locations under folders that exist on this machine — ~/Pictures
	// is a macOS/Windows convention; a Linux box without it won't offer it.
	var outSuggestions []string
	for _, c := range []string{
		filepath.Join(homeDir, "Pictures", "WanderSort"),
		filepath.Join(homeDir, "WanderSortLibrary"),
	} {
		if st, err := os.Stat(filepath.Dir(c)); err == nil && st.IsDir() {
			outSuggestions = append(outSuggestions, c)
		}
	}
	// suggestOut completes like a shell: list directories matching the typed
	// prefix. Blank input falls back to the canned platform locations.
	suggestOut := func(typed string) []string {
		typed = expandHome(strings.TrimSpace(typed))
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
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if strings.HasPrefix(strings.ToLower(e.Name()), strings.ToLower(base)) {
				out = append(out, filepath.Join(dir, e.Name()))
				if len(out) == 5 {
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
		// FullName ("Indore, Madhya Pradesh, India"), not the bare city: a list
		// of six identical "Springfield"s is unpickable, and the state/country
		// are exactly what tells them apart. The saved town keeps that form —
		// location.ResolveByName resolves it back to the right row.
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
	// examplePath builds the folder path the answers *so far* would produce, so
	// every example on screen reacts to the rules ticked earlier instead of
	// showing a canned tree that may include levels the user just turned off.
	// day/town are the caller's stand-ins for those two levels ("" drops the
	// level, which is exactly what "date only" and an unresolved location mean);
	// collapsed drops the device/orientation/media levels the way the collapse
	// setting does.
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
	// homeTown is what the home/work examples name; a stand-in until the user
	// has typed a town, so the example is never blank.
	homeTown := func() string {
		if t := strings.TrimSpace(home); t != "" {
			return t
		}
		return "Indore"
	}
	rulesField.Example = func() string { return examplePath("02", "Goa", false) }

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
			Kind:  tui.FieldConfirm,
			Title: "Collapse uninformative levels?",
			Description: "Drop a device/orientation/media folder that would hold every single\n" +
				"file in the library — one value means the level says nothing.",
			BoolValue: &collapse,
			Example: func() string {
				// With no device/orientation/media level ticked there is nothing
				// to collapse, and both answers would render the same path —
				// say so instead of showing an example that doesn't move.
				if !rulesField.Selected[vfs.RuleDevice] && !rulesField.Selected[vfs.RuleOrientation] && !rulesField.Selected[vfs.RuleMedia] {
					return examplePath("02", "Goa", collapse) + "\n(no device/orientation/media level ticked — nothing to collapse)"
				}
				return examplePath("02", "Goa", collapse)
			},
		},
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
					Kind:  tui.FieldConfirm,
					Title: "Group home/work photos by date only?",
					Description: "Everyday shots from home/work aren't trips — a city folder there\n" +
						"mostly repeats itself.",
					BoolValue: &hwDateOnly,
					Example: func() string {
						if hwDateOnly {
							return examplePath("12", "", collapse) + "   (no city folder)"
						}
						return examplePath("12", homeTown(), collapse) + "   (suburbs fold into " + homeTown() + ")"
					},
				},
				{
					Kind:  tui.FieldConfirm,
					Title: "Merge consecutive same-location days?",
					Description: "A multi-day trip in one place becomes one folder instead of one per\n" +
						"day. You can still split or merge days later in review.",
					BoolValue: &mergeDays,
					Example: func() string {
						if mergeDays {
							return examplePath("02_04", "Goa", collapse)
						}
						return examplePath("02", "Goa", collapse) + "\n" +
							examplePath("03", "Goa", collapse) + "\n" +
							examplePath("04", "Goa", collapse)
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
			OutputPath:            expandHome(strings.TrimSpace(out)),
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

// toMap converts a slice to a map for multiselect field initialization.
func toMap(items []string) map[string]bool {
	m := make(map[string]bool)
	for _, item := range items {
		m[item] = true
	}
	return m
}

// canonicalTown returns the gazetteer's exact spelling of a typed town name, or
// an error if it has no case-insensitive match. Blank input is an error too
// (callers treat blank as "skip" before calling for the canonical form).
func (a *App) canonicalTown(ctx context.Context, typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" || a.LocationResolver == nil {
		return "", fmt.Errorf("no town")
	}
	matches, err := a.LocationResolver.SearchByName(ctx, typed, 8)
	if err != nil || len(matches) == 0 {
		return "", fmt.Errorf("no town named %q — check the spelling", typed)
	}
	if name, ok := exactMatch(matches, typed); ok {
		return name, nil
	}
	return "", fmt.Errorf("no exact match for %q (did you mean %s?)", typed, matches[0].Name)
}
