package cli

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/install"
	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// fieldByTitle finds a form field by its title so the tests don't pin the
// wizard's field order.
func fieldByTitle(t *testing.T, fields []*tui.Field, title string) *tui.Field {
	t.Helper()
	f := findField(fields, title)
	if f == nil {
		t.Fatalf("no %q field in the wizard", title)
	}
	return f
}

// findField is fieldByTitle for the cases where the field's absence is the
// thing being asserted.
func findField(fields []*tui.Field, title string) *tui.Field {
	for _, f := range fields {
		if f.Title == title {
			return f
		}
	}
	return nil
}

func TestConfig(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// The first-run setup asks library, layout (a custom rule list opens the
		// rule step) and home, and its save writes every setting and creates
		// the library.
		{"SetupSavesEverySetting", func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)

			cfg := testConfig(t)
			cfg.Rules = []string{"date", "device"}
			cfg.CollapseLevels = false
			a := &app{Config: cfg, Log: logger.NewNoopLogger(), logFile: logger.NewFile(t.TempDir())}
			defer a.closeDBs()

			// a place-name database that never opened must not trap the user
			// on the town, nor drop it
			broken := errors.New("location db: database is locked")
			fields, save := a.buildSettingsForm(context.Background(), func() (*location.Resolver, error) { return nil, broken })
			var titles []string
			for _, f := range fields {
				titles = append(titles, f.Title)
			}
			want := []string{"Where should your library go?", "How should folders be laid out?", "Which folders, in order?", "Where's home?"}
			if !reflect.DeepEqual(titles, want) {
				t.Errorf("setup steps = %v, want %v", titles, want)
			}
			layout := fieldByTitle(t, fields, "How should folders be laid out?")
			if *layout.Value != customLayout {
				t.Errorf("rules %v should open as Custom, got %q", cfg.Rules, *layout.Value)
			}
			if fieldByTitle(t, fields, "Which folders, in order?").Skip() {
				t.Error("a custom layout must ask which folders")
			}

			homeField := fieldByTitle(t, fields, "Where's home?")
			if err := homeField.Validator("  "); err != nil {
				t.Errorf("blank town must stay skippable, got %v", err)
			}
			*homeField.Value = "Indore"
			if err := homeField.Validator("Indore"); err != nil {
				t.Errorf("unusable place names must let a town through, got %v", err)
			}
			if err := save(); err != nil {
				t.Fatalf("save: %v", err)
			}
			got, err := config.LoadSettings(context.Background(), a.AppDB)
			if err != nil {
				t.Fatal(err)
			}
			wantSettings := config.Settings{
				Rules:                 []string{"date", "device"},
				CollapseLevels:        false,
				SavedPlacesDateOnly:   cfg.SavedPlacesDateOnly,
				MergeSameLocationDays: cfg.MergeSameLocationDays,
				HomeTown:              "Indore",
				WorkTown:              "Indore", // blank work = same as home
			}
			if !got.Equal(wantSettings) {
				t.Fatalf("saved settings = %+v, want %+v", got, wantSettings)
			}
			if _, err := os.Stat(cfg.AppDBPath); err != nil {
				t.Errorf("the save must create the library it writes to: %v", err)
			}
		}},
		{"LayoutPresetsMapToRules", func(t *testing.T) {
			for _, tt := range []struct {
				rules  []string
				choice string
				label  string
			}{
				{nil, layoutPresets[0].name + recommended, "Year › Month › Day › Place"},
				{[]string{"date", "location"}, layoutPresets[0].name + recommended, "Year › Month › Day › Place"},
				{[]string{"location"}, "Year › Month › Place", "Year › Month › Place"},
				{[]string{"none"}, "Year › Month", "Year › Month"},
				{[]string{"date", "device"}, customLayout, "Custom: Year › Month › Day › Camera"},
			} {
				if got := layoutChoice(tt.rules); got != tt.choice {
					t.Errorf("layoutChoice(%v) = %q, want %q", tt.rules, got, tt.choice)
				}
				if got := layoutLabel(tt.rules); got != tt.label {
					t.Errorf("layoutLabel(%v) = %q, want %q", tt.rules, got, tt.label)
				}
			}

			a := &app{Config: testConfig(t), Log: logger.NewNoopLogger()}
			f := a.newSettingsForm(context.Background(), func() (*location.Resolver, error) { return nil, install.ErrPending })
			f.layout = "Year › Month › Place"
			if got := f.rules(); !reflect.DeepEqual(got, []string{"location"}) {
				t.Errorf("preset rules = %v, want [location]", got)
			}
			f.layout = customLayout
			f.rulesField.Selected = map[string]bool{}
			if got := f.rules(); !reflect.DeepEqual(got, []string{"none"}) {
				t.Errorf("a custom layout with nothing ticked = %v, want [none]", got)
			}
		}},
		// An open library's settings are a list; each row edits a fresh copy, so
		// leaving an edit unsaved changes nothing.
		{"SettingsListEditsOneRow", func(t *testing.T) {
			a := &app{Config: testConfig(t), Log: logger.NewNoopLogger(), logFile: logger.NewFile(t.TempDir())}
			if err := a.openLibrary(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer a.closeDBs()
			rows := a.settingsRows(context.Background(), func() (*location.Resolver, error) { return nil, install.ErrPending })()
			var labels []string
			for _, r := range rows {
				labels = append(labels, r.Label)
			}
			if want := []string{"Library folder", "Folder layout", "Home", "Work", "Fine-tuning"}; !reflect.DeepEqual(labels, want) {
				t.Fatalf("rows = %v, want %v", labels, want)
			}
			if rows[0].Edit != nil {
				t.Error("an open library's folder can't change")
			}

			fields, _ := rows[4].Edit()
			group := fields[0]
			// the collapse example shows all three collapsible levels when off
			*group.Subs[0].BoolValue = false
			if ex := group.Subs[0].Example(); !strings.Contains(ex, "iPhone-13") || !strings.Contains(ex, "Vertical") {
				t.Errorf("collapse-off example must show the collapsible levels, got %q", ex)
			}
			// edited but never saved: the next edit starts from the saved settings
			again, save := rows[4].Edit()
			if !*again[0].Subs[0].BoolValue {
				t.Error("an unsaved edit leaked into the next one")
			}
			*again[0].Subs[2].BoolValue = false
			if err := save(); err != nil {
				t.Fatal(err)
			}
			if a.Config.MergeSameLocationDays {
				t.Error("saving the fine-tuning row must write the new answer")
			}
		}},
		{"OutputPathAskedOnlyBeforeTheLibraryIsOpen", func(t *testing.T) {
			a := &app{Config: testConfig(t), Log: logger.NewNoopLogger(), logFile: logger.NewFile(t.TempDir())}
			fields, _ := a.buildSettingsForm(context.Background(), func() (*location.Resolver, error) { return nil, install.ErrPending })
			if f := findField(fields, "Where should your library go?"); f == nil {
				t.Error("a session with no library open must be asked for the library folder")
			}

			if err := a.openLibrary(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer a.closeDBs()
			fields, _ = a.buildSettingsForm(context.Background(), func() (*location.Resolver, error) { return nil, install.ErrPending })
			if f := findField(fields, "Where should your library go?"); f != nil {
				t.Error("an open library's folder is fixed — the setup must not offer to change it")
			}
		}},
		// TestTownFieldsRoundTripARealTown exercises the real geonames path that
		// every other case in this file leaves untouched by passing a nil
		// resolver: with a ready resolver and geonames() reporting no error,
		// suggestTown/townValidator/Canonical must round-trip a real
		// town through SearchByName/exactMatch to its canonical geonames spelling.
		{"TownFieldsRoundTripARealTown", func(t *testing.T) {
			// Resolve against the real machine's ~/.wandersort/location.db
			// *before* HOME gets redirected below — installtest.Resolver reads
			// $HOME itself, and pointing it at a fresh temp dir would make it
			// re-download the ~80MB database instead of reusing the copy
			// already cached on this machine.
			resolver := installtest.Resolver(t)

			cfg := testConfig(t)
			a := &app{Config: cfg, Log: logger.NewNoopLogger(), logFile: logger.NewFile(t.TempDir())}
			defer a.closeDBs()

			fields, save := a.buildSettingsForm(context.Background(), func() (*location.Resolver, error) { return resolver, nil })
			homeField := fieldByTitle(t, fields, "Where's home?")

			// A partial real city name must surface a real geonames entry — the
			// full "city, state, country" form the picker always lists.
			suggestions := homeField.Suggest("Indo")
			found := false
			for _, s := range suggestions {
				if s == "Indore, Madhya Pradesh, India" {
					found = true
				}
			}
			if !found {
				t.Errorf("suggestions for %q = %v, want to include %q", "Indo", suggestions, "Indore, Madhya Pradesh, India")
			}

			// A typed name the geonames database knows validates clean...
			if err := homeField.Validator("indore"); err != nil {
				t.Errorf("known town must validate, got %v", err)
			}
			// ...and a name it has never heard of is accepted as typed — the
			// geonames database missing a village must not trap the user on this field.
			if err := homeField.Validator("Nowhereville Not A Real Town"); err != nil {
				t.Errorf("unknown town must validate as typed, got %v", err)
			}
			if got, err := resolver.Canonical(context.Background(), "Nowhereville Not A Real Town"); err != nil || got != "Nowhereville Not A Real Town" {
				t.Errorf("Canonical(unknown) = %q, %v, want the typed name back", got, err)
			}

			// Canonical resolves the typed spelling to the geonames own — the
			// full "city, state, country" form.
			got, err := resolver.Canonical(context.Background(), "indore")
			if err != nil {
				t.Fatalf("Canonical(%q): %v", "indore", err)
			}
			if want := "Indore, Madhya Pradesh, India"; got != want {
				t.Errorf("Canonical(%q) = %q, want %q", "indore", got, want)
			}

			*homeField.Value = "indore"
			if err := save(); err != nil {
				t.Fatalf("save: %v", err)
			}
			g, err := config.LoadSettings(context.Background(), a.AppDB)
			if err != nil {
				t.Fatal(err)
			}
			if g.HomeTown != "Indore, Madhya Pradesh, India" {
				t.Errorf("saved home town = %q, want the canonical geonames spelling", g.HomeTown)
			}
		}},
		// TestTreeExample covers the wizard's example renderer: sibling paths must
		// fold their shared prefix into one branch point (the merge-days "no"
		// example), and the note lands on the last line only.
		{"TreeExample", func(t *testing.T) {
			got := treeExample("(note)",
				"2024/08_August/02/IMG_1.jpg",
				"2024/08_August/03/IMG_2.jpg")
			want := "2024\n" +
				"└─ 08_August\n" +
				"   ├─ 02\n" +
				"   │  └─ IMG_1.jpg\n" +
				"   └─ 03\n" +
				"      └─ IMG_2.jpg\n" +
				"\n" +
				"(note)"
			if got != want {
				t.Errorf("treeExample:\n%s\nwant:\n%s", got, want)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
