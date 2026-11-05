package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// layoutPresets are the ready-made folder layouts; any other rule list is Custom.
var layoutPresets = []struct {
	name  string
	rules []string
}{
	{"Year › Month › Day › Place", []string{vfs.RuleDate, vfs.RuleLocation}},
	{"Year › Month › Place", []string{vfs.RuleLocation}},
	{"Year › Month › Day", []string{vfs.RuleDate}},
}

// customLayout is the layout choice that opens the full rule list.
const customLayout = "Custom…"

// layoutChoice is the option for rules: a preset's name (no rules = the first), or customLayout.
func layoutChoice(rules []string) string {
	if len(rules) == 0 {
		return layoutPresets[0].name
	}
	for _, p := range layoutPresets {
		if slices.Equal(p.rules, rules) {
			return p.name
		}
	}
	return customLayout
}

// layoutLabel is how the settings list shows a rule list.
func layoutLabel(rules []string) string {
	switch c := layoutChoice(rules); c {
	case layoutPresets[0].name:
		return "Default"
	case customLayout:
		return "Custom: " + layoutName(rules, " › ")
	default:
		return c
	}
}

// settingsForm is one edit of a library's settings, built fresh so leaving without saving changes nothing.
type settingsForm struct {
	ctx      context.Context
	a        *app
	geonames func() (*location.Resolver, error)
	paths    *path.Resolver

	rulesField *tui.Field
	ex         *settingsExamples

	out                           string
	layout                        string
	home                          string
	collapse, dateOnly, mergeDays bool
}

func (a *app) newSettingsForm(ctx context.Context, geonames func() (*location.Resolver, error)) *settingsForm {
	s := a.Config.Settings
	paths := path.New()
	f := &settingsForm{
		a: a, ctx: ctx, geonames: geonames, paths: paths,
		// prefilled with the last library, or the default one
		out:      paths.RelativeToHome(a.Config.OutputDir()),
		layout:   layoutChoice(s.Rules),
		collapse: s.CollapseLevels, dateOnly: s.SavedPlacesDateOnly, mergeDays: s.MergeSameLocationDays,
		home: s.HomeTown,
	}
	custom := s.Rules
	if len(custom) == 0 || slices.Equal(custom, []string{vfs.RuleNone}) {
		custom = vfs.DefaultConfig().Rules
	}
	f.rulesField = &tui.Field{
		Kind:     tui.FieldMultiSelect,
		Title:    "Which folders, in order?",
		Options:  []string{vfs.RuleDate, vfs.RuleLocation, vfs.RuleDevice, vfs.RuleOrientation, vfs.RuleMedia},
		Selected: toMap(custom),
		Description: "Folder levels below Year and Month. date = day, location = place, " +
			"device = camera, orientation = portrait or landscape, media = photo or video.",
		Skip: func() bool { return f.layout != customLayout },
	}
	f.ex = newSettingsExamples(f.rules, &f.collapse, &f.mergeDays, &f.dateOnly, &f.home)
	f.rulesField.Example = f.ex.Rules
	return f
}

// rules is the folder levels the form's answers stand for.
func (f *settingsForm) rules() []string {
	for _, p := range layoutPresets {
		if f.layout == p.name {
			return p.rules
		}
	}
	var out []string
	for _, opt := range f.rulesField.Options {
		if f.rulesField.Selected[opt] {
			out = append(out, opt)
		}
	}
	if len(out) == 0 {
		return []string{vfs.RuleNone}
	}
	return out
}

// libraryDir is the folder typed, pasted or dropped into the library field.
func (f *settingsForm) libraryDir(typed string) string {
	if dirs := f.paths.Dropped(typed); len(dirs) == 1 {
		return dirs[0]
	}
	return f.paths.ExpandPath(strings.TrimSpace(typed))
}

func (f *settingsForm) libraryField() *tui.Field {
	homeDir := f.paths.HomeDir
	// recently used libraries first, then common locations whose parent exists
	var suggestions []string
	for _, dir := range f.a.Config.History() {
		suggestions = append(suggestions, f.paths.RelativeToHome(dir))
	}
	for _, c := range []string{
		filepath.Join(homeDir, "Pictures", "WanderSort"),
		filepath.Join(homeDir, config.DefaultLibrary),
	} {
		rel := f.paths.RelativeToHome(c)
		if st, err := os.Stat(filepath.Dir(c)); err == nil && st.IsDir() && !slices.Contains(suggestions, rel) {
			suggestions = append(suggestions, rel)
		}
	}
	return &tui.Field{
		Kind:        tui.FieldInput,
		Title:       "Which library?",
		Description: "Drop a folder here, or type its path. An empty folder starts a new library.",
		Value:       &f.out,
		Suggest: func(typed string) []string {
			if strings.TrimSpace(typed) == "" {
				return suggestions
			}
			return suggestDirs(f.paths, typed)
		},
		Validator: func(s string) error {
			return config.CheckLibrary(f.libraryDir(s))
		},
	}
}

func (f *settingsForm) layoutFields() []*tui.Field {
	options := make([]string, 0, len(layoutPresets)+1)
	for _, p := range layoutPresets {
		options = append(options, p.name)
	}
	options = append(options, customLayout)
	return []*tui.Field{{
		Kind:        tui.FieldSelect,
		Title:       "How should folders be laid out?",
		Description: "You can change this later. Files already copied stay where they are.",
		Options:     options,
		Recommended: layoutPresets[0].name,
		Value:       &f.layout,
		Example:     f.ex.Rules,
	}, f.rulesField}
}

// townField is a town input that completes from the locationDB.
func (f *settingsForm) townField(title, description string, value *string) *tui.Field {
	return &tui.Field{
		Kind: tui.FieldInput, Title: title, Description: description,
		Placeholder: "a town, or enter to skip",
		Value:       value,
		Validator:   f.validateTown,
		Suggest: func(typed string) []string {
			typed = strings.TrimSpace(typed)
			resolver, err := f.geonames()
			if len(typed) < 2 || err != nil {
				return nil
			}
			return resolver.SuggestNames(f.ctx, typed, 6)
		},
	}
}

func (f *settingsForm) homeField() *tui.Field {
	return f.townField("Where's home?",
		"Everyday photos from home get a date folder, without the town's name on every day. Optional.", &f.home)
}

func (f *settingsForm) fineTuning() *tui.Field {
	return &tui.Field{
		Kind:  tui.FieldGroup,
		Title: "Fine-tuning",
		Subs: []*tui.Field{
			{
				Kind: tui.FieldConfirm, Title: "Skip folders that would all be the same?",
				Describe: f.ex.CollapseDescribe, BoolValue: &f.collapse, Example: f.ex.Collapse,
			},
			{
				Kind: tui.FieldConfirm, Title: "Date only for home?",
				Describe: f.ex.DateOnlyDescribe, BoolValue: &f.dateOnly, Example: f.ex.DateOnly,
			},
			{
				Kind: tui.FieldConfirm, Title: "Merge back-to-back days in one place?",
				Describe: f.ex.MergeDaysDescribe, BoolValue: &f.mergeDays, Example: f.ex.MergeDays,
			},
		},
	}
}

// validateTown rejects a typo but accepts an unknown name, or any name when the locationDB failed to open.
func (f *settingsForm) validateTown(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil // blank = skip
	}
	resolver, err := f.geonames()
	if err != nil {
		if errors.Is(err, install.ErrPending) {
			return err
		}
		return nil
	}
	_, err = resolver.Canonical(f.ctx, s)
	return err
}

// canonicalTown is the locationDB's spelling when they can give one, else
// what was typed: dropping a town the user already had is data loss.
func (f *settingsForm) canonicalTown(typed string) string {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return ""
	}
	resolver, err := f.geonames()
	if err != nil {
		return typed
	}
	name, err := resolver.Canonical(f.ctx, typed)
	switch {
	case err == nil:
		return name
	case errors.Is(err, location.ErrNotExact):
		return "" // a near-miss the validator would have rejected
	default:
		return typed
	}
}

// save writes the answers, opening or creating the library if the form asked where it goes.
func (f *settingsForm) save() error {
	home := f.canonicalTown(f.home)
	s := config.Settings{
		Rules:                 f.rules(),
		CollapseLevels:        f.collapse,
		SavedPlacesDateOnly:   f.dateOnly,
		MergeSameLocationDays: f.mergeDays,
		HomeTown:              home,
		WorkTown:              home, // work is off for now: it is the same place as home
	}
	if err := f.a.saveSettings(f.ctx, f.libraryDir(f.out), s); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

// settingsRows is an open library's settings, one row each; every edit starts from the saved settings.
func (a *app) settingsRows(ctx context.Context, geonames func() (*location.Resolver, error)) func() []tui.SettingRow {
	edit := func(fields func(*settingsForm) []*tui.Field) func() ([]*tui.Field, func() error) {
		return func() ([]*tui.Field, func() error) {
			f := a.newSettingsForm(ctx, geonames)
			return fields(f), f.save
		}
	}
	return func() []tui.SettingRow {
		s := a.Config.Settings
		orNotSet := func(v string) string {
			if v == "" {
				return "not set"
			}
			return v
		}
		toggles := []bool{s.CollapseLevels, s.SavedPlacesDateOnly, s.MergeSameLocationDays}
		on := 0
		for _, b := range toggles {
			if b {
				on++
			}
		}
		tuning := fmt.Sprintf("%d of %d on", on, len(toggles))
		if on == len(toggles) {
			tuning = "all on"
		}
		return []tui.SettingRow{
			{Label: "Library folder", Value: path.New().RelativeToHome(a.Config.OutputDir()), Edit: func() ([]*tui.Field, func() error) {
				f := a.newSettingsForm(ctx, geonames)
				return []*tui.Field{f.libraryField()}, func() error {
					return a.switchLibrary(ctx, f.libraryDir(f.out))
				}
			}},
			{Label: "Folder layout", Value: layoutLabel(s.Rules), Edit: edit((*settingsForm).layoutFields)},
			{Label: "Home", Value: orNotSet(s.HomeTown), Edit: edit(func(f *settingsForm) []*tui.Field { return []*tui.Field{f.homeField()} })},
			{Label: "Fine-tuning", Value: tuning, Edit: edit(func(f *settingsForm) []*tui.Field { return []*tui.Field{f.fineTuning()} })},
		}
	}
}

// ruleNames is how each folder level reads in a layout.
var ruleNames = map[string]string{
	vfs.RuleDate:        "Day",
	vfs.RuleLocation:    "Place",
	vfs.RuleDevice:      "Camera",
	vfs.RuleOrientation: "Orientation",
	vfs.RuleMedia:       "Photo/Video",
}

// layoutName spells a library's folder levels, Year and Month first.
func layoutName(rules []string, sep string) string {
	parts := []string{"Year", "Month"}
	for _, r := range rules {
		if n, ok := ruleNames[r]; ok {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, sep)
}

// toMap converts a slice to a map for multiselect field initialization.
func toMap(items []string) map[string]bool {
	m := make(map[string]bool)
	for _, item := range items {
		m[item] = true
	}
	return m
}
