package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Clickable text carries the keys a left click on it presses, hidden in
// zero-width APC sequences that terminals ignore. The shell reads them back
// from the rendered view with ClickTargets, so a screen gets mouse support by
// drawing its hints and choices through Clickable.
const (
	clickOpen = "\x1b_ws:"
	clickEnd  = "\x1b\\"
)

// Clickable wraps text so a click on it presses keys, named as KeyMsgFor reads them.
func Clickable(text string, keys ...string) string {
	if len(keys) == 0 {
		return text
	}
	return clickOpen + strings.Join(keys, " ") + clickEnd + text + clickOpen + clickEnd
}

// ClickTarget is the cells [From, To) of one view row and the keys a click there presses.
type ClickTarget struct {
	Row, From, To int
	Keys          []string
}

// ClickTargets lists every Clickable span in view, left to right, top to
// bottom. A span cut by truncation or an overlay has no target.
func ClickTargets(view string) []ClickTarget {
	var targets []ClickTarget
	for row, line := range strings.Split(view, "\n") {
		var keys []string
		from := -1
		for at := 0; ; {
			i := strings.Index(line[at:], clickOpen)
			if i < 0 {
				break
			}
			i += at
			j := strings.Index(line[i:], clickEnd)
			if j < 0 {
				break
			}
			payload := line[i+len(clickOpen) : i+j]
			at = i + j + len(clickEnd)
			switch {
			case payload != "":
				keys, from = strings.Fields(payload), ansi.StringWidth(line[:i])
			case from >= 0:
				targets = append(targets, ClickTarget{Row: row, From: from, To: ansi.StringWidth(line[:i]), Keys: keys})
				from = -1
			}
		}
	}
	return targets
}

// ClickAt is the target drawn at column x of row, or false when there is none.
func ClickAt(targets []ClickTarget, row, x int) (ClickTarget, bool) {
	for _, t := range targets {
		if t.Row == row && x >= t.From && x < t.To {
			return t, true
		}
	}
	return ClickTarget{}, false
}

var keyTypes = map[string]tea.KeyType{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
}

// KeyMsgFor builds the key press a name stands for: enter, esc, tab, shift+tab,
// space, an arrow, ctrl+<letter>, or one character.
func KeyMsgFor(name string) tea.KeyMsg {
	if t, ok := keyTypes[name]; ok {
		return tea.KeyMsg{Type: t}
	}
	if name == "space" {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	if letter, ok := strings.CutPrefix(name, "ctrl+"); ok && len(letter) == 1 && letter[0] >= 'a' && letter[0] <= 'z' {
		return tea.KeyMsg{Type: tea.KeyCtrlA + tea.KeyType(letter[0]-'a')}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// MoveKeys is the arrow presses that take a list cursor from one row to another.
func MoveKeys(from, to int) []string {
	key, n := "down", to-from
	if n < 0 {
		key, n = "up", -n
	}
	return slices.Repeat([]string{key}, n)
}

// hintKeys is the keys a footer hint's key label stands for; labels that name
// a range or a choice ("1-3", "↑↓", "y/n") are not one key, so they are not clickable.
func hintKeys(label string) []string {
	if label == "any key" {
		return []string{"enter"}
	}
	if strings.ContainsAny(label, "↑↓←→/-") {
		return nil
	}
	return []string{label}
}
