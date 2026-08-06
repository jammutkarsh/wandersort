// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (m Model) View() string {
	if m.askRebuild {
		return m.rebuildAskView()
	}
	if m.showHelp {
		return m.helpView()
	}
	var b strings.Builder
	b.WriteString(m.header())

	selLo, selHi := -1, -1
	if m.visualMode {
		selLo, selHi = m.visualAnchor, m.cursor
		if selLo > selHi {
			selLo, selHi = selHi, selLo
		}
	}

	end := min(m.offset+m.visibleRows(), len(m.rows))
	for i := m.offset; i < end; i++ {
		b.WriteString("\n")
		b.WriteString(m.rowView(i, selLo != -1 && i >= selLo && i <= selHi))
	}

	return tui.Screen(b.String(), m.footer(), m.height)
}

// header is the banner plus one summary line: what this screen is on the left,
// what it holds on the right — the same left/right split every screen uses.
func (m Model) header() string {
	files := 0
	for i := range m.tree {
		files += m.tree[i].FileCount
	}
	return tui.Banner("review") + "\n" +
		tui.Row(tui.DimText.Render("Edit the proposed folders — nothing moves until you save."),
			tui.FaintTxt.Render(fmt.Sprintf("%d folders  %d files", len(m.rows), files)), m.width)
}

// rowView renders one tree line: guide + name, file count right-aligned. The
// name shown is the node's own — a rename is written straight onto it, so
// there is no "old → new" or "suggested" state to render.
func (m Model) rowView(i int, inRange bool) string {
	r := m.rows[i]

	cursor := "  "
	count := fmt.Sprintf("%d files", r.node.FileCount)

	if inRange || i == m.cursor {
		// Plain, no per-segment colour — a nested ANSI reset would cut the highlight short.
		if i == m.cursor {
			cursor = "❯ "
		}
		return tui.Selected.Render(tui.Row(cursor+r.guide+r.node.Name, count, m.width))
	}

	return tui.Row(cursor+tui.FaintTxt.Render(r.guide)+r.node.Name, tui.FaintTxt.Render(count), m.width)
}

// footer is everything below the tree: the rename prompt, a spinner, or the
// status line plus key help. Height varies with width, so visibleRows measures
// it rather than assuming one line.
func (m Model) footer() string {
	var b strings.Builder
	switch {
	case m.editing:
		fmt.Fprintf(&b, "%s%s%s█\n",
			tui.DimText.Render("Rename "+m.rows[m.cursor].node.Name+" to"),
			tui.Title.Render(" » "), tui.Text.Render(m.input))
		for i, s := range m.suggestions {
			// same shape as the wizard's completion list: dim rows, the arrowed-onto
			// one highlighted, and the top match — what [tab] fills in — says so
			line := "    "
			if i == m.suggCursor {
				line += tui.Selected.Render(s.Label)
			} else {
				line += tui.FaintTxt.Render("· ") + tui.DimText.Render(s.Label)
			}
			if s.Detail != "" {
				line += " " + tui.FaintTxt.Render("("+s.Detail+")")
			}
			switch {
			case i == m.suggCursor:
				line += tui.FaintTxt.Render("  ⏎ pick")
			case i == 0 && m.suggCursor < 0:
				line += tui.FaintTxt.Render("  ⇥ tab")
			}
			fmt.Fprintln(&b, tui.Row(line, "", m.width))
		}
		b.WriteString(tui.Footer(strings.Join([]string{
			tui.KeyHint("enter", "apply"),
			tui.KeyHint("↑↓", "pick a place"),
			tui.KeyHint("tab", "use top match"),
			tui.KeyHint("ctrl+e", "wider search"),
			tui.KeyHint("esc", "cancel"),
		}, "   "), m.width))
	case m.previewing:
		b.WriteString(m.spin.View())
		b.WriteString(tui.DimText.Render(" Copying preview…"))
	case m.rebuilding:
		b.WriteString(m.spin.View())
		b.WriteString(tui.DimText.Render(" Re-proposing folders with your current settings…"))
	default:
		if m.previewErr != nil {
			fmt.Fprintln(&b, tui.Bad.Render("Preview failed: ")+tui.Text.Render(m.previewErr.Error()))
		}
		if m.visualMode {
			fmt.Fprintln(&b, tui.Title.Render(fmt.Sprintf("-- SELECT -- %d folders", len(m.selectedRows()))))
		}
		if m.statusMsg != "" {
			if m.statusIsErr {
				fmt.Fprintln(&b, tui.Attn.Render("⚠ "+m.statusMsg))
			} else {
				fmt.Fprintln(&b, m.wrapDim(m.statusMsg))
			}
		}
		b.WriteString(tui.Footer(m.keyHelp(), m.width))
	}
	return b.String()
}

// keyHelp is the key bar, styled like every other screen's (key in the brand
// colour, action dim) and ordered by how a review actually goes: move, name,
// reshape, leave.
func (m Model) keyHelp() string {
	hints := []string{
		tui.KeyHint("↑↓", "move"),
		tui.KeyHint("n/N", "same level"),
		tui.KeyHint("r", "rename"),
		tui.KeyHint("p", "peek"),
	}
	if m.visualMode {
		hints = append(hints,
			tui.KeyHint("m", "merge"),
			tui.KeyHint("d", "drop"),
			tui.KeyHint("D", "flatten"),
			tui.KeyHint("esc", "clear selection"))
	} else {
		hints = append(hints,
			tui.KeyHint("V", "select"),
			tui.KeyHint("d", "drop"),
			tui.KeyHint("D", "flatten"))
	}
	hints = append(hints, tui.KeyHint("u", "undo"))
	if m.rebuild != nil {
		hints = append(hints, tui.KeyHint("R", "rebuild"))
	}
	hints = append(hints, tui.KeyHint("c", "save & exit"), tui.KeyHint("ctrl+c", "discard"),
		tui.KeyHint("?", "help"))
	return strings.Join(hints, "   ")
}

// rebuildAskView is the rebuild question, drawn as the same full-screen yes/no
// dialog `wandersort reset` asks with — a settings change invalidates the whole
// plan on screen, so it gets the screen, not a line above the key bar that a
// reviewer reading the tree will never look at.
//
// The ConfirmModel is built per frame rather than stored: it is pure layout,
// and a bubbletea model copied by value can't safely hold a pointer into its
// own fields, which is what its Value is.
func (m Model) rebuildAskView() string {
	choice := m.rebuildChoice
	c := tui.NewConfirmModel("Settings changed since this plan was proposed", m.rebuildAskText(), &choice)
	sized, _ := c.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	return sized.View()
}

// rebuildAskText spells out what a rebuild costs. A stamp is a hash, so it
// can't name which setting moved — only the two kinds that can.
func (m Model) rebuildAskText() string {
	text := "Your folder rules or saved places changed, so this plan no longer matches them.\n" +
		"Rebuild re-proposes every folder from the new settings. No keeps the plan as it is — [R] asks again."
	switch {
	case m.hasEdits() && m.approvedFiles > 0:
		text += fmt.Sprintf("\n\nYour unsaved edits and %d already-approved files will be re-proposed.", m.approvedFiles)
	case m.hasEdits():
		text += "\n\nYour unsaved edits will be discarded."
	case m.approvedFiles > 0:
		text += fmt.Sprintf("\n\n%d already-approved files will be re-proposed.", m.approvedFiles)
	}
	return text
}

// helpView is the full-screen key reference behind [?] — the footer names the
// keys, this explains them. Any key returns to the tree.
func (m Model) helpView() string {
	type key struct{ k, what string }
	sections := []struct {
		title string
		keys  []key
	}{
		{"Moving", []key{
			{"↑/↓  j/k", "move one row"},
			{"n / N", "next / previous folder at the same depth — crosses into other branches"},
		}},
		{"Naming", []key{
			{"r", "rename — type a name, or ↑/↓ to a nearby place; tab fills the top match"},
		}},
		{"Reshaping", []key{
			{"V", "start selecting folders; move the cursor to extend, esc to clear"},
			{"m", "merge the selected folders into one, under their common parent"},
			{"d", "drop the folder — its contents move up one level, the folder goes away"},
			{"D", "flatten — everything below moves directly into the folder"},
			{"u", "undo the last reshape; press again to walk further back"},
			{"R", "rebuild — re-propose every folder from your current settings, discarding your edits"},
		}},
		{"Leaving", []key{
			{"p", "peek — copies a sample of the folder's files and opens them (read-only)"},
			{"c", "save the plan and exit — the only key that writes anything"},
			{"ctrl+c", "exit without saving (warns once if you have unsaved edits)"},
		}},
	}

	var b strings.Builder
	b.WriteString(tui.Banner("review · help"))
	for _, s := range sections {
		fmt.Fprintf(&b, "\n\n %s", tui.Title.Render(s.title))
		for _, k := range s.keys {
			// pad by display width, not bytes — the arrow keys are multibyte
			pad := strings.Repeat(" ", max(12-lipgloss.Width(k.k), 2))
			fmt.Fprintf(&b, "\n%s", tui.Row("   "+tui.Text.Render(k.k)+pad+tui.DimText.Render(k.what), "", m.width))
		}
	}
	return tui.Screen(b.String(), tui.Footer(tui.KeyHint("any key", "back to the tree"), m.width), m.height)
}
