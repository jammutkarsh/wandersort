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

// MoreKeys is the hint every footer ends on: the rest of the keys are one ? away.
func MoreKeys() string { return KeyHint("?", "more keys") }

// keyHelpBox is the bordered key list the overlay draws.
var keyHelpBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Primary).Padding(0, 2)

// KeyHelp draws groups in a box centred over base, side by side when one column is too tall.
func KeyHelp(base string, groups []KeyGroup, w, h int) string {
	blocks := make([]string, len(groups))
	for i, g := range groups {
		var b strings.Builder
		b.WriteString(Title.Render(g.Title))
		for _, k := range g.Keys {
			pad := strings.Repeat(" ", max(8-ansi.StringWidth(k.Key), 2))
			b.WriteString("\n" + Text.Render(k.Key) + pad + DimText.Render(k.What))
		}
		blocks[i] = b.String()
	}
	body := strings.Join(blocks, "\n\n")
	const chrome = 6 // border, padding, the closing hint
	if h > 0 && lipgloss.Height(body)+chrome > h && len(blocks) > 1 {
		half := (len(blocks) + 1) / 2
		left := lipgloss.NewStyle().PaddingRight(4).Render(strings.Join(blocks[:half], "\n\n"))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Join(blocks[half:], "\n\n"))
	}
	body += "\n\n" + FaintTxt.Render("any key closes")
	return overlay(base, keyHelpBox.Render(body), w, h)
}

// overlay draws box over base, centred, keeping base's lines either side.
func overlay(base, box string, w, h int) string {
	lines := strings.Split(base, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	boxLines := strings.Split(box, "\n")
	boxW := lipgloss.Width(box)
	if w > 0 && boxW > w {
		for i := range boxLines {
			boxLines[i] = ansi.Truncate(boxLines[i], w, "")
		}
		boxW = w
	}
	x := max((w-boxW)/2, 0)
	y := max((h-len(boxLines))/2, 1)
	if h > 0 && len(boxLines) > h {
		boxLines = boxLines[:h] // a terminal too short for the list shows what fits
		y = 0
	}
	for i, bl := range boxLines {
		row := y + i
		if h > 0 && row >= h {
			break
		}
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
