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
	if c := layoutChoice(rules); c != customLayout {
		return c
	}
	return "Custom: " + layoutName(rules, " › ")
}

// settingsForm is one edit of a library's settings, built fresh so leaving without saving changes nothing.
type settingsForm struct {
	a        *app
	ctx      context.Context
	geonames func() (*location.Resolver, error)
	paths    *path.Resolver

	out                           string
	layout                        string
	rulesField                    *tui.Field
	collapse, dateOnly, mergeDays bool
	home, work                    string
	ex                            *settingsExamples
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
		home: s.HomeTown, work: s.WorkTown,
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
		if st, err := os.Stat(filepath.Dir(c)); err == nil && st.IsDir() {
			suggestions = append(suggestions, f.paths.RelativeToHome(c))
		}
	}
	return &tui.Field{
		Kind:        tui.FieldInput,
		Title:       "Where should your library go?",
		Description: "An empty folder, or one WanderSort has organised before.",
		Value:       &f.out,
		Suggest: func(typed string) []string {
			if strings.TrimSpace(typed) == "" {
				return suggestions
			}
			return suggestDirs(f.paths, typed)
		},
		Validator: func(s string) error {
			return config.CheckLibrary(f.paths.ExpandPath(strings.TrimSpace(s)))
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

func (f *settingsForm) workField() *tui.Field {
	return f.townField("Where's work?",
		"Treated like home. Leave it empty if it's the same town.", &f.work)
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
				Kind: tui.FieldConfirm, Title: "Date only for home and work?",
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
	work := f.work
	if strings.TrimSpace(work) == "" {
		work = f.home // blank work = same as home
	}
	s := config.Settings{
		Rules:                 f.rules(),
		CollapseLevels:        f.collapse,
		SavedPlacesDateOnly:   f.dateOnly,
		MergeSameLocationDays: f.mergeDays,
		HomeTown:              f.canonicalTown(f.home),
		WorkTown:              f.canonicalTown(work),
	}
	if err := f.a.saveSettings(f.ctx, f.paths.ExpandPath(strings.TrimSpace(f.out)), s); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

// buildSettingsForm is the first-run setup: library folder (while none is open), layout and home.
func (a *app) buildSettingsForm(ctx context.Context, geonames func() (*location.Resolver, error)) ([]*tui.Field, func() error) {
	f := a.newSettingsForm(ctx, geonames)
	var fields []*tui.Field
	if a.AppDB == nil {
		fields = append(fields, f.libraryField())
	}
	fields = append(fields, f.layoutFields()...)
	fields = append(fields, f.homeField())
	return fields, f.save
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
		work := s.WorkTown
		if work == s.HomeTown {
			work = ""
		}
		return []tui.SettingRow{
			{Label: "Library folder", Value: path.New().RelativeToHome(a.Config.OutputDir()), Edit: func() ([]*tui.Field, func() error) {
				f := a.newSettingsForm(ctx, geonames)
				field := f.libraryField()
				field.Title = "Which library?"
				field.Description = "A folder WanderSort has organised opens as it is. Any other folder starts a new library with these settings. Files planned here don't come along; add their folders again there. This library stays as it is."
				return []*tui.Field{field}, func() error {
					return a.switchLibrary(ctx, f.paths.ExpandPath(strings.TrimSpace(f.out)))
				}
			}},
			{Label: "Folder layout", Value: layoutLabel(s.Rules), Edit: edit((*settingsForm).layoutFields)},
			{Label: "Home", Value: orNotSet(s.HomeTown), Edit: edit(func(f *settingsForm) []*tui.Field { return []*tui.Field{f.homeField()} })},
			{Label: "Work", Value: orNotSet(work), Edit: edit(func(f *settingsForm) []*tui.Field { return []*tui.Field{f.workField()} })},
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
