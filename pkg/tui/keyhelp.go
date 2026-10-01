package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// KeyGroup is one heading of the ? overlay and the keys under it.
type KeyGroup struct {
	Title string
	Keys  []KeyLine
}

// KeyLine is one key and what it does, in a few words.
type KeyLine struct{ Key, What string }

// MoreKeys is the footer hint every screen ends on: the rest of its keys are
// one ? away.
func MoreKeys() string { return KeyHint("?", "more keys") }

// keyHelpBox is the bordered key list the overlay draws.
var keyHelpBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Primary).Padding(0, 2)

// KeyHelp draws groups in a bordered box over base, centred in a w×h screen,
// so the screen stays visible around it.
func KeyHelp(base string, groups []KeyGroup, w, h int) string {
	var b strings.Builder
	for i, g := range groups {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(Title.Render(g.Title))
		for _, k := range g.Keys {
			pad := strings.Repeat(" ", max(10-ansi.StringWidth(k.Key), 2))
			b.WriteString("\n" + Text.Render(k.Key) + pad + DimText.Render(k.What))
		}
	}
	b.WriteString("\n\n" + FaintTxt.Render("any key closes"))
	return overlay(base, keyHelpBox.Render(b.String()), w, h)
}

// overlay draws box over base, centred, keeping base's lines either side.
func overlay(base, box string, w, h int) string {
	lines := strings.Split(base, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	boxLines := strings.Split(box, "\n")
	boxW := lipgloss.Width(box)
	x := max((w-boxW)/2, 0)
	y := max((h-len(boxLines))/2, 1)
	for i, bl := range boxLines {
		row := y + i
		if row >= len(lines) {
			lines = append(lines, "")
		}
		under := lines[row]
		left := ansi.Truncate(under, x, "")
		left += strings.Repeat(" ", max(x-ansi.StringWidth(left), 0))
		right := ansi.TruncateLeft(under, x+boxW, "")
		// reset between the pieces so a highlighted row's colour can't run
		// into the box
		lines[row] = fmt.Sprintf("%s\x1b[0m%s\x1b[0m%s", left, bl, right)
	}
	return strings.Join(lines, "\n")
}
