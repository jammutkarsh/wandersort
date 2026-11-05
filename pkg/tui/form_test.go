package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// leaveOf reports the Leave a screen handed back, if it did. Every hosted
// screen ends this way now — none of them quits the program the container
// owns.
func leaveOf(cmd tea.Cmd) (Leave, bool) {
	for _, msg := range flattenCmd(cmd) {
		if l, ok := msg.(Leave); ok {
			return l, true
		}
	}
	return Leave{}, false
}

// flattenCmd executes cmd (and recursively any tea.Batch it produces),
// returning every resulting message in order.
func flattenCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, flattenCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// sendKey feeds a keystroke through Update and drives the debounced Suggest
// round-trip to completion.
func sendKey(m FormModel, k tea.KeyMsg) FormModel {
	next, cmd := m.Update(k)
	m = next.(FormModel)
	for _, msg := range flattenCmd(cmd) {
		if _, ok := msg.(suggestDebounceMsg); !ok {
			continue
		}
		next, cmd := m.Update(msg)
		m = next.(FormModel)
		for _, msg := range flattenCmd(cmd) {
			if _, ok := msg.(suggestResultMsg); !ok {
				continue
			}
			next, _ := m.Update(msg)
			m = next.(FormModel)
		}
	}
	return m
}

func TestForm(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// The form renders as one top-down stack: answered fields collapse to a
		// summary row with their value, the current field expands, pending fields
		// stay visible but dim.
		{"FormStackedView", func(t *testing.T) {
			out := "/tmp/lib"
			yes := true
			fields := []*Field{
				{Kind: FieldInput, Title: "Output path", Value: &out},
				{Kind: FieldConfirm, Title: "Collapse levels?", BoolValue: &yes},
				{Kind: FieldMultiSelect, Title: "Rules", Options: []string{"date", "location"}, Selected: map[string]bool{"date": true}},
			}
			m := NewFormModel(fields, nil)
			m.Current = 2 // first two answered

			view := ansi.Strip(m.View())
			for _, want := range []string{"/tmp/lib", "yes", "date", "location", "Output path", "Collapse levels?", "Rules"} {
				if !strings.Contains(view, want) {
					t.Errorf("stacked view missing %q:\n%s", want, view)
				}
			}
		}},
		// A FieldGroup answers its sub-inputs in order on one screen; the collapsed
		// summary joins the answers.
		{"FormGroupField", func(t *testing.T) {
			home, work := "", ""
			done := false
			fields := []*Field{{Kind: FieldGroup, Title: "Towns", Subs: []*Field{
				{Kind: FieldInput, Title: "Home", Value: &home},
				{Kind: FieldInput, Title: "Work", Value: &work},
			}}}
			m := NewFormModel(fields, func() error { done = true; return nil })

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Delhi")})
			m = next.(FormModel)
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(FormModel)
			if home != "Delhi" || m.subIdx != 1 {
				t.Fatalf("enter should commit sub 0 and focus sub 1: home=%q subIdx=%d", home, m.subIdx)
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Goa")})
			m = next.(FormModel)
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(FormModel)
			if work != "Goa" || !done {
				t.Fatalf("enter on last sub should finish the form: work=%q done=%v", work, done)
			}
			if got := m.summaryValue(fields[0]); got != "Delhi / Goa" {
				t.Errorf("group summary = %q, want \"Delhi / Goa\"", got)
			}
		}},
		// ↓ highlights a suggestion, enter picks it instead of advancing the form.
		{"FormSuggestArrowPick", func(t *testing.T) {
			v := ""
			fields := []*Field{{Kind: FieldInput, Title: "Town", Value: &v, Suggest: func(typed string) []string {
				if typed == "" {
					return nil
				}
				return []string{"Delhi", "Dehradun"}
			}}}
			m := NewFormModel(fields, nil)

			m = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
			for _, k := range []tea.KeyMsg{
				{Type: tea.KeyDown},
				{Type: tea.KeyDown},
				{Type: tea.KeyEnter},
			} {
				next, _ := m.Update(k)
				m = next.(FormModel)
			}
			if v != "Dehradun" {
				t.Errorf("↓↓ + enter should pick the second suggestion, got %q", v)
			}
			if m.Current != 0 {
				t.Errorf("picking a suggestion must not advance the form")
			}
		}},
		{"FormSuggestAndTabFill", func(t *testing.T) {
			v := ""
			fields := []*Field{{Kind: FieldInput, Title: "Town", Value: &v, Suggest: func(typed string) []string {
				if typed == "" {
					return nil
				}
				return []string{"Delhi", "Dehradun"}
			}}}
			m := NewFormModel(fields, nil)

			m = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
			if len(m.sugg) != 2 {
				t.Fatalf("typing should refresh suggestions, got %v", m.sugg)
			}
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
			m = next.(FormModel)
			if v != "Delhi" {
				t.Errorf("tab should fill top suggestion, got %q", v)
			}
		}},
		// Examples render outside the description, only for the choice under the
		// cursor.
		{"FormExample", func(t *testing.T) {
			dateOnly := true
			town := ""
			fields := []*Field{{
				Kind:  FieldGroup,
				Title: "Saved places",
				Subs: []*Field{
					{Kind: FieldInput, Title: "Home town", Value: &town},
					{
						Kind: FieldConfirm, Title: "Date only?", Description: "explanation only",
						BoolValue: &dateOnly,
						Example: func() string {
							if dateOnly {
								return "2024/08/12/IMG.jpg"
							}
							return "2024/08/12/Indore/IMG.jpg"
						},
					},
				},
			}}
			m := NewFormModel(fields, nil)
			m.w, m.h = 140, 30

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(FormModel)
			if m.subIdx != 1 {
				t.Fatalf("enter should reach the confirm sub, subIdx=%d", m.subIdx)
			}

			// → picks no, without advancing — and only that option's example renders,
			// outside the description.
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
			m = next.(FormModel)
			if dateOnly {
				t.Error("right should select no")
			}
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "2024/08/12/Indore/IMG.jpg") || strings.Contains(view, "2024/08/12/IMG.jpg\n") {
				t.Errorf("only the selected option's example should render:\n%s", view)
			}
			if strings.Contains(view, "explanation only\n    2024/") {
				t.Errorf("example must not sit inside the description:\n%s", view)
			}
		}},
		// Nothing is numbered, and a digit is ordinary input (or ignored): it
		// never jumps to a step.
		{"FormHasNoNumbers", func(t *testing.T) {
			out := ""
			rules := map[string]bool{"date": true}
			fields := []*Field{
				{Kind: FieldInput, Title: "Output path", Value: &out},
				{Kind: FieldMultiSelect, Title: "Rules", Options: []string{"date", "location"}, Selected: rules},
			}
			m := NewFormModel(fields, nil)
			m.w, m.h = 80, 24

			view := ansi.Strip(m.View())
			if !strings.Contains(view, "Output path") || !strings.Contains(view, "Rules") {
				t.Errorf("both steps should show:\n%s", view)
			}
			if strings.Contains(view, "1)") || strings.Contains(view, "2)") {
				t.Errorf("no step or option may be numbered:\n%s", view)
			}

			// A digit typed into the focused text field is ordinary input.
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
			m = next.(FormModel)
			if m.Current != 0 || out != "2" {
				t.Errorf("digit in a text field must type: Current=%d out=%q", m.Current, out)
			}

			// Past the text field, a digit no longer jumps to that step.
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(FormModel)
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
			m = next.(FormModel)
			if m.Current != 1 {
				t.Errorf("digit must not jump steps: Current=%d", m.Current)
			}
		}},
		// FormExampleSidePanel: on a wide terminal the example renders as a
		// right-hand column (bordered box); a narrow one has no room for it, so
		// it is left out.
		{"FormExampleSidePanel", func(t *testing.T) {
			yes := true
			m := NewFormModel([]*Field{{
				Kind: FieldConfirm, Title: "Merge?", BoolValue: &yes,
				Example: func() string { return "2024\n└─ 08_August" },
			}}, nil)

			m.w, m.h = 140, 30
			if !m.sidePanel() {
				t.Fatal("wide terminal with an example should use the side panel")
			}
			wide := ansi.Strip(m.View())
			if !strings.Contains(wide, "└─ 08_August") || !strings.Contains(wide, "╭") {
				t.Errorf("side panel missing example box:\n%s", wide)
			}

			m.w = 80
			if m.sidePanel() {
				t.Fatal("narrow terminal must not get the side panel")
			}
			if narrow := ansi.Strip(m.View()); strings.Contains(narrow, "08_August") {
				t.Errorf("a narrow terminal must not show the example:\n%s", narrow)
			}
		}},
		// Embedded in the app shell, the form never quits the program — the
		// container owns it, and quitting would take the running scan down
		// with the wizard.
		{"FormHandsBackInsteadOfQuitting", func(t *testing.T) {
			for _, tc := range []struct {
				name string
				keys []tea.KeyMsg
			}{
				{"abort", []tea.KeyMsg{{Type: tea.KeyCtrlC}}},
				// esc raises the Save/Discard question rather than saving
				// straight away — "y" answers Save.
				{"save & exit", []tea.KeyMsg{{Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune("y")}}},
				{"last field", []tea.KeyMsg{{Type: tea.KeyEnter}}},
			} {
				saved := false
				yes := true
				m := NewFormModel([]*Field{{Kind: FieldConfirm, Title: "Only step", BoolValue: &yes}},
					func() error { saved = true; return nil })

				var left bool
				for _, key := range tc.keys {
					next, cmd := m.Update(key)
					m = next.(FormModel)
					for _, msg := range flattenCmd(cmd) {
						if msg == tea.Quit() {
							t.Fatalf("%s: the form quit the program the container owns", tc.name)
						}
					}
					if _, ok := leaveOf(cmd); ok {
						left = true
					}
				}
				if !left {
					t.Errorf("%s: the form should hand back with tui.Leave", tc.name)
				}
				if tc.name != "abort" && !saved {
					t.Errorf("%s: should still have submitted", tc.name)
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// TestFormExitAskDiscardVsSave: esc asks Save/Discard, and the two answers
// differ.
func TestFormExitAskDiscardVsSave(t *testing.T) {
	newForm := func() (FormModel, *bool) {
		saved := false
		yes := true
		m := NewFormModel([]*Field{{Kind: FieldConfirm, Title: "Only step", BoolValue: &yes}},
			func() error { saved = true; return nil })
		return m, &saved
	}

	t.Run("esc alone only asks, does not save", func(t *testing.T) {
		m, saved := newForm()
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		fm := next.(FormModel)
		if _, left := leaveOf(cmd); left || *saved {
			t.Error("esc must raise the question, not save or exit on its own")
		}
		if !fm.askExit {
			t.Error("esc must set askExit")
		}
	})

	t.Run("n discards without saving", func(t *testing.T) {
		m, saved := newForm()
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		_, cmd := next.(FormModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
		l, left := leaveOf(cmd)
		if !left || !l.Aborted || *saved {
			t.Errorf("n must abort without saving: left=%v leave=%+v saved=%v", left, l, *saved)
		}
		if l.Quit {
			t.Error("n means done here, not done with the app")
		}
	})

	t.Run("y saves", func(t *testing.T) {
		m, saved := newForm()
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		_, cmd := next.(FormModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
		l, left := leaveOf(cmd)
		if !left || l.Aborted || !*saved {
			t.Errorf("y must save and exit: left=%v leave=%+v saved=%v", left, l, *saved)
		}
	})

	t.Run("ctrl+c bypasses the question and discards immediately", func(t *testing.T) {
		m, saved := newForm()
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		_, cmd := next.(FormModel).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		l, left := leaveOf(cmd)
		if !left || !l.Aborted || *saved {
			t.Error("ctrl+c must discard immediately, even with the question up")
		}
		// ctrl+c is the one key that means the session, not the screen.
		if !l.Quit {
			t.Error("ctrl+c should ask for the session to end")
		}
	})
}

// A description wraps to the body width instead of losing its tail to
// truncation, and Describe wins over Description when both are set.
func TestFormDescriptionBlock(t *testing.T) {
	m := FormModel{w: 100} // side panel showing: body is well under 100 cols
	long := "one two three four five six seven eight nine ten eleven twelve thirteen fourteen"
	f := &Field{Description: "static", Describe: func() string { return long }}

	got := m.descriptionBlock(f, 4)
	if strings.Contains(got, "…") {
		t.Errorf("description truncated:\n%s", got)
	}
	if strings.Contains(got, "static") {
		t.Error("Describe should override Description")
	}
	for _, word := range strings.Fields(long) {
		if !strings.Contains(got, word) {
			t.Errorf("word %q dropped from:\n%s", word, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w > m.bodyW() {
			t.Errorf("line width %d > body %d: %q", w, m.bodyW(), line)
		}
	}
	if m.descriptionBlock(&Field{}, 4) != "" {
		t.Error("empty description should render nothing")
	}
}

func TestFormSummaryValues(t *testing.T) {
	m := FormModel{}
	blank := "  "
	no := false
	tests := []struct {
		name  string
		field *Field
		want  string
	}{
		{"blank input", &Field{Kind: FieldInput, Value: &blank}, "—"},
		{"confirm", &Field{Kind: FieldConfirm, BoolValue: &no}, "no"},
		{
			"multiselect keeps option order, not selection order",
			&Field{Kind: FieldMultiSelect, Options: []string{"a", "b", "c"}, Selected: map[string]bool{"c": true, "a": true}},
			"a, c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.summaryValue(tt.field); got != tt.want {
				t.Errorf("summaryValue = %q, want %q", got, tt.want)
			}
		})
	}
}

// A form that has finished waits for its host to take it away; keys landing in
// that gap (enter pressed twice) must neither crash nor submit again.
func TestFormIgnoresKeysAfterItFinishes(t *testing.T) {
	v := "x"
	submits := 0
	m := NewFormModel([]*Field{{Kind: FieldInput, Title: "Only", Value: &v}}, func() error { submits++; return nil })
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyEnter}, {Type: tea.KeyEnter}, {Type: tea.KeyCtrlB}, {Type: tea.KeyEsc}, {Type: tea.KeyEnter},
	} {
		next, _ := m.Update(k)
		m = next.(FormModel)
	}
	if submits != 1 {
		t.Errorf("onSubmit ran %d times, want once", submits)
	}
}

// From the middle question of a group, a click on the first or the third
// walks there without the keyboard.
func TestFormGroupQuestionsAreClickable(t *testing.T) {
	a, b, c := true, true, true
	m := NewFormModel([]*Field{{Kind: FieldGroup, Title: "Extra", Subs: []*Field{
		{Kind: FieldConfirm, Title: "First?", BoolValue: &a},
		{Kind: FieldConfirm, Title: "Second?", BoolValue: &b},
		{Kind: FieldConfirm, Title: "Third?", BoolValue: &c},
	}}}, func() error { return nil })
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(FormModel)

	keysFor := func(title string) string {
		t.Helper()
		for _, tg := range ClickTargets(m.View()) {
			if strings.Contains(ansi.Strip(strings.Split(m.View(), "\n")[tg.Row]), title) {
				return strings.Join(tg.Keys, " ")
			}
		}
		return ""
	}
	if got := keysFor("First?"); got != "ctrl+b" {
		t.Errorf("first question from the second: keys = %q, want ctrl+b", got)
	}
	if got := keysFor("Third?"); got != "enter" {
		t.Errorf("third question from the second: keys = %q, want enter", got)
	}
}

// Every yes/no question of a group shows its own buttons; a click on one walks
// to the question and picks that side without moving on.
func TestFormGroupButtonsAnswerWhereTheyStand(t *testing.T) {
	a, b, c := true, true, true
	m := NewFormModel([]*Field{{Kind: FieldGroup, Title: "Extra", Subs: []*Field{
		{Kind: FieldConfirm, Title: "First?", BoolValue: &a},
		{Kind: FieldConfirm, Title: "Second?", BoolValue: &b},
		{Kind: FieldConfirm, Title: "Third?", BoolValue: &c},
	}}}, func() error { return nil })
	m.w = 120

	var third []string
	for _, tg := range ClickTargets(m.View()) {
		if line := ansi.Strip(strings.Split(m.View(), "\n")[tg.Row]); strings.Contains(line, "Third?") && tg.Keys[len(tg.Keys)-1] == "right" {
			third = tg.Keys
		}
	}
	if got := strings.Join(third, " "); got != "enter enter right" {
		t.Fatalf("no on the third question from the first: keys = %q, want enter enter right", got)
	}

	for _, key := range third {
		next, _ := m.Update(KeyMsgFor(key))
		m = next.(FormModel)
	}
	if !a || !b || c {
		t.Errorf("answers = %v %v %v, want yes yes no", a, b, c)
	}
}

// A narrow screen has no side panel, so no example; the description still sits above the footer.
func TestFormNarrowScreenDropsTheExample(t *testing.T) {
	on := true
	fields := []*Field{{Kind: FieldGroup, Title: "Extra", Subs: []*Field{
		{
			Kind: FieldConfirm, Title: "First?", BoolValue: &on, Describe: func() string { return "what the first does" },
			Example: func() string { return "2024/08/IMG.jpg" },
		},
		{Kind: FieldConfirm, Title: "Second?", BoolValue: &on},
	}}}
	m := NewFormModel(fields, nil)
	m.w, m.h = 80, 30

	narrow := viewText(m.View())
	if strings.Contains(narrow, "2024/08/IMG.jpg") {
		t.Errorf("narrow: the example must go:\n%s", narrow)
	}
	if !strings.Contains(narrow, "what the first does") || !strings.Contains(narrow, "Second?") {
		t.Errorf("narrow: the description and every question must stay:\n%s", narrow)
	}

	m.w = 140
	if wide := viewText(m.View()); !strings.Contains(wide, "2024/08/IMG.jpg") {
		t.Errorf("wide: the example belongs in the side panel:\n%s", wide)
	}
}

// ↑/↓ move between a group's yes/no questions without changing any answer;
// ←/→ answer the focused one.
func TestFormGroupArrowsMoveAndAnswerSeparately(t *testing.T) {
	a, b, c := true, true, true
	m := NewFormModel([]*Field{{Kind: FieldGroup, Title: "Extra", Subs: []*Field{
		{Kind: FieldConfirm, Title: "First?", BoolValue: &a},
		{Kind: FieldConfirm, Title: "Second?", BoolValue: &b},
		{Kind: FieldConfirm, Title: "Third?", BoolValue: &c},
	}}}, nil)

	var model tea.Model = m
	press := func(keys ...string) FormModel {
		for _, k := range keys {
			model, _ = model.Update(KeyMsgFor(k))
		}
		return model.(FormModel)
	}
	if got := press("down", "down"); got.subIdx != 2 || !a || !b || !c {
		t.Fatalf("two downs: subIdx=%d answers=%v %v %v, want 2 and nothing changed", got.subIdx, a, b, c)
	}
	if got := press("down"); got.subIdx != 2 {
		t.Errorf("down past the last question moved to %d", got.subIdx)
	}
	if got := press("up", "right"); got.subIdx != 1 || !a || b || !c {
		t.Errorf("up then right: subIdx=%d answers=%v %v %v, want 1 with only the second set to no", got.subIdx, a, b, c)
	}
	if got := press("up", "up", "left"); got.subIdx != 0 || !a {
		t.Errorf("up past the first question: subIdx=%d a=%v", got.subIdx, a)
	}
}

// A form that has finished stays drawable until its host swaps it out, and
// keys that land in that gap must not index past the last field.
func TestFormSurvivesKeysAfterItFinished(t *testing.T) {
	town := ""
	submits := 0
	m := NewFormModel([]*Field{{Kind: FieldInput, Title: "Home", Value: &town}}, func() error { submits++; return nil })
	m.w, m.h = 100, 30

	var model tea.Model = m
	for _, key := range []string{"enter", "enter", "ctrl+b", "esc", "enter"} {
		model, _ = model.Update(KeyMsgFor(key))
		_ = model.View()
	}
	if submits != 1 {
		t.Errorf("submits = %d, want exactly 1", submits)
	}
}

func TestFormSurvivesKeysAfterAGroupFinished(t *testing.T) {
	on := true
	m := NewFormModel([]*Field{{Kind: FieldGroup, Title: "Extra", Subs: []*Field{
		{Kind: FieldConfirm, Title: "First?", BoolValue: &on},
		{Kind: FieldConfirm, Title: "Second?", BoolValue: &on},
	}}}, nil)
	m.w, m.h = 100, 30

	var model tea.Model = m
	for _, key := range []string{"enter", "enter", "enter", "ctrl+b", "enter"} {
		model, _ = model.Update(KeyMsgFor(key))
		_ = model.View()
	}
}

// A path typed in full, with its folders offered below it, is accepted by
// enter; enter only takes a lone completion that finishes what was typed.
func TestFormEnterDoesNotDrillIntoAFinishedPath(t *testing.T) {
	out := ""
	m := NewFormModel([]*Field{
		{Kind: FieldInput, Title: "Library", Value: &out, Suggest: func(typed string) []string {
			switch strings.TrimSpace(typed) {
			case "~/lib/2025":
				return []string{"~/lib/2025/12_December"} // the folder's own child
			case "~/li":
				return []string{"~/lib"} // finishes what was typed
			}
			return nil
		}},
		{Kind: FieldInput, Title: "Home", Value: new(string)},
	}, nil)

	var model tea.Model = m
	typeText := func(s string) {
		for _, r := range s {
			model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		// suggestions arrive after the debounce; hand the form its answer
		f := model.(FormModel)
		f.sugg = f.Fields[0].Suggest(f.ti.Value())
		model = f
	}

	typeText("~/lib/2025 ")
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := model.(FormModel); got.Current != 1 || out != "~/lib/2025 " {
		t.Fatalf("enter on a finished path: Current=%d out=%q, want to move on with it untouched", got.Current, out)
	}

	out = ""
	model = NewFormModel(m.Fields, nil)
	typeText("~/li")
	model, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := model.(FormModel); got.Current != 0 || got.ti.Value() != "~/lib" {
		t.Errorf("enter on a prefix: Current=%d value=%q, want the lone completion taken", got.Current, got.ti.Value())
	}
}
