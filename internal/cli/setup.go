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
	"golang.org/x/term"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/exiftool"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/spf13/cobra"
)

func (a *App) newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Configure WanderSort and download its dependencies",
		Long: `Downloads exiftool and the location database, then walks you through a
short setup wizard (output folder, workers, folder grouping, home/work towns).
Everything it asks is optional and has a default — scan and serve install any
missing dependency on first use, so setup is a convenience, not a requirement.
Safe to re-run. In a non-interactive shell only the dependencies install;
edit settings with 'wandersort config' instead.`,
		Example: "wandersort setup",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSetup()
		},
	}
}

func (a *App) runSetup() error {
	ctx := context.Background()

	// A running scan/serve installs dependencies itself and takes precedence, so
	// if anything is already installing, step aside instead of installing twice.
	l, err := lock.AcquireInstall(ctx, a.installDir(), false)
	if errors.Is(err, lock.ErrHeld) {
		a.Log.Info("Dependencies are already being installed by another process; nothing to do", logger.UserKey, true)
		return nil
	}
	if err != nil {
		return fmt.Errorf("install lock: %w", err)
	}
	defer l.Unlock()

	// Setup is TUI-only: without a terminal (piped/redirected stderr) the wizard
	// can't run, so just install dependencies and point at `wandersort config`.
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		if err := a.installDeps(ctx); err != nil {
			return err
		}
		a.Log.Info("Dependencies installed. No interactive terminal — edit settings with 'wandersort config'.", logger.UserKey, true)
		return nil
	}

	// Full-screen: install screen (progress bars) that swaps straight into the
	// wizard inside one alt-screen program — no terminal flash between them.
	switch err := a.runSetupTUI(ctx); {
	case errors.Is(err, errSetupCancelled):
		// Quit the form without saving — say only that, not "complete".
		a.Log.Info("Setup cancelled; nothing saved.", logger.UserKey, true)
		return nil
	case err != nil:
		return err
	}
	a.Log.Info("Setup complete. You're ready to scan.", logger.UserKey, true)
	return nil
}

// installDeps downloads exiftool + the location database (both no-ops if already
// present) and opens the resolver. The setup TUI wraps this so its downloads
// render as progress bars; the plain path calls it directly.
func (a *App) installDeps(ctx context.Context) error {
	if _, err := exiftool.Setup(ctx, a.Log, a.Config.ExecutablePath, a.progressFor("exiftool")); err != nil {
		return fmt.Errorf("exiftool: %w", err)
	}
	if err := a.InitLocationResolver(ctx); err != nil {
		return fmt.Errorf("location db: %w", err)
	}
	return nil
}

// runSetupTUI runs dependency install and the setup wizard as one alt-screen
// program: the install screen swaps in-place to the wizard once downloads
// finish (InstallModel.Next), so there's no terminal-restore flash between the
// two. The wizard is built lazily inside Next because its town validator needs
// the location resolver that installDeps opens. Byte progress flows through
// InstallProgress (a callback, not the logger — the file log only sees
// start/done milestones); those milestones reach the screen through a TUI
// logger sink. a.Log and InstallProgress are restored before returning.
func (a *App) runSetupTUI(ctx context.Context) error {
	// Route all logging to the file (via the TUI sink) for the whole run —
	// a second `setup` used to print every install "found/verified" line to
	// the plain console and only then launch the alt-screen over it (every
	// line, with debug on). The console never sees install chatter here.
	events := make(chan logger.Event, 256)
	origLog := a.Log
	a.Log = logger.NewTUI(a.Config.LogLevel, a.Config.LogFile, func(e logger.Event) { events <- e })
	defer func() {
		a.Log = origLog
		a.InstallProgress = nil
		close(events)
	}()

	var formFields []*tui.Field
	var save func() error
	buildWizard := func() (tea.Model, error) {
		formFields, save = a.buildSetupForm(ctx) // resolver is open now — installDeps ran
		return tui.NewFormModel(formFields, save), nil
	}

	// Deps already present → skip the install screen entirely: its stages would
	// flip to "done" instantly and that flash reads as a flicker. Open the
	// resolver quietly (installDeps downloads nothing here) and show the wizard
	// alone.
	var first tea.Model
	if exiftool.Installed(a.Log, a.Config.ExecutablePath) && location.Installed(a.Config.LocationDBPath) {
		if err := a.installDeps(ctx); err != nil {
			return err
		}
		first, _ = buildWizard()
	} else {
		install := tui.NewInstallModel(func() error { return a.installDeps(ctx) })
		install.Next = buildWizard
		first = install
	}

	prog := tea.NewProgram(tui.NewShell(first), tea.WithAltScreen(), tea.WithOutput(os.Stderr))
	a.InstallProgress = func(phase string, done, total int64) {
		prog.Send(tui.InstallProgressMsg{Phase: phase, Done: done, Total: total})
	}
	go func() {
		for e := range events {
			prog.Send(tui.LogEventMsg{Event: e})
		}
	}()

	final, runErr := prog.Run()
	if runErr != nil {
		return fmt.Errorf("setup ui: %w", runErr)
	}

	// formFields is nil only if the wizard was never reached: install errored, or the
	// user quit (ctrl+c) mid-install. Either way, nothing was saved.
	if formFields == nil {
		if s, ok := final.(tui.Shell); ok {
			if im, ok := s.Current().(tui.InstallModel); ok && im.Err() != nil {
				return im.Err()
			}
		}
		return errSetupCancelled
	}
	if s, ok := final.(tui.Shell); ok {
		if fm, ok := s.Current().(tui.FormModel); ok && fm.IsAborted() {
			return errSetupCancelled
		}
	}
	return nil
}

// errSetupCancelled signals the user quit the setup wizard (ctrl+c) so runSetup
// reports only "cancelled", not a bogus "complete" afterward.
var errSetupCancelled = errors.New("setup cancelled")

// buildSetupForm builds form fields (seeded with current effective values) and
// a save closure that writes the user's choices back to ~/.wandersort/config.yaml
// (comments preserved — see config.SaveSettings).
func (a *App) buildSetupForm(ctx context.Context) ([]*tui.Field, func() error) {
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
	debug := a.Config.LogLevel == "debug"
	home := g.HomeWork.Home
	work := g.HomeWork.Work

	// townValidator rejects a town the gazetteer doesn't know, so the saved
	// place always resolves later (same guarantee as the pickPlace flow).
	townValidator := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return nil // blank = skip
		}
		if _, err := a.canonicalTown(ctx, s); err != nil {
			return err
		}
		return nil
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
	// only ever get names the location DB can resolve later.
	suggestTown := func(typed string) []string {
		typed = strings.TrimSpace(typed)
		if len(typed) < 2 || a.LocationResolver == nil {
			return nil
		}
		matches, err := a.LocationResolver.SearchByName(ctx, typed, 6)
		if err != nil {
			return nil
		}
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.Name
		}
		return names
	}

	rulesField := &tui.Field{
		Kind:     tui.FieldMultiSelect,
		Title:    "Rules",
		Options:  []string{vfs.RuleDate, vfs.RuleLocation, vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia},
		Selected: toMap(groupBy),
	}
	// The example path tracks the selection live, in nesting order.
	ruleSeg := map[string]string{
		vfs.RuleDate:        "02",
		vfs.RuleLocation:    "Goa",
		vfs.RuleDevice:      "iPhone 13",
		vfs.RuleOrientation: "Vertical",
		vfs.RuleMedia:       "Photos",
	}
	rulesExample := func() string {
		segs := []string{"2024", "08_August"}
		for _, r := range rulesField.Options {
			if rulesField.Selected[r] {
				segs = append(segs, ruleSeg[r])
			}
		}
		return strings.Join(segs, "/") + "/IMG_1234.jpg"
	}
	rulesField.Describe = func() string {
		return "Folder levels below Year/Month, in nesting order.\n" +
			"  date = day    location = city    device = camera\n" +
			"  orientation = portrait/landscape    media = photo/video\n" +
			"Your tree:  " + rulesExample()
	}

	// pick marks the example line matching the current yes/no choice.
	pick := func(line string, active bool) string {
		if active {
			return "» " + line
		}
		return "  " + line
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
			Kind:  tui.FieldConfirm,
			Title: "Collapse uninformative levels?",
			Describe: func() string {
				return "Drop a device/orientation/media folder that would hold every single\n" +
					"file in the library — one value means the level says nothing.\n" +
					pick("yes:  2024/08_August/Goa/  (iPhone 13/Vertical/Photos all dropped)", collapse) + "\n" +
					pick("no:   2024/08_August/Goa/iPhone 13/Vertical/Photos/", !collapse)
			},
			BoolValue: &collapse,
		},
		{
			Kind:  tui.FieldConfirm,
			Title: "Merge consecutive same-location days?",
			Describe: func() string {
				return "A multi-day trip in one place becomes one folder instead of one per day.\n" +
					pick("yes:  2024/08_August/02_04/Goa/", mergeDays) + "\n" +
					pick("no:   2024/08_August/02/Goa/   03/Goa/   04/Goa/", !mergeDays) + "\n" +
					"You can still split or merge days later in review."
			},
			BoolValue: &mergeDays,
		},
		{
			Kind:      tui.FieldConfirm,
			Title:     "Enable debug logging?",
			BoolValue: &debug,
		},
		{
			Kind:        tui.FieldGroup,
			Title:       "Home & work towns",
			Description: "The everyday places you shoot from — the next question decides how\nphotos taken there are foldered. Blank to skip.",
			Subs: []*tui.Field{
				{Kind: tui.FieldInput, Title: "Home", Placeholder: "e.g. Delhi (blank to skip)", Value: &home, Validator: townValidator, Suggest: suggestTown},
				{Kind: tui.FieldInput, Title: "Work", Placeholder: "blank = same as home", Value: &work, Validator: townValidator, Suggest: suggestTown},
			},
		},
		{
			Kind:  tui.FieldConfirm,
			Title: "Group home/work photos by date only?",
			Describe: func() string {
				town := strings.TrimSpace(home)
				if town == "" {
					town = "Delhi"
				}
				return "Everyday shots from home/work aren't trips — a city folder there\n" +
					"mostly repeats itself.\n" +
					pick("yes:  2024/08_August/12/IMG_1234.jpg   (date only, no city folder)", hwDateOnly) + "\n" +
					pick(fmt.Sprintf("no:   2024/08_August/12/%s/IMG_1234.jpg   (suburbs fold into %s)", town, town), !hwDateOnly)
			},
			BoolValue: &hwDateOnly,
		},
	}

	// save runs after the form completes. It reads the bound vars this form
	// wrote and persists them.
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
		s := config.Settings{
			OutputPath:       expandHome(strings.TrimSpace(out)),
			Rules:            selectedRules,
			Collapse:         &collapse,
			HomeWorkDateOnly: &hwDateOnly,
			MergeDays:        &mergeDays,
			Debug:            &debug,
		}
		if w, err := strconv.Atoi(strings.TrimSpace(workers)); err == nil {
			s.Workers = w
		}
		// Canonicalize towns to the exact gazetteer spelling before saving.
		if name, err := a.canonicalTown(ctx, home); err == nil {
			s.Home = name
		}
		if name, err := a.canonicalTown(ctx, work); err == nil {
			s.Work = name
		}
		if err := config.SaveSettings(s); err != nil {
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
