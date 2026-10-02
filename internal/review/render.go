package review

import (
	"fmt"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/human"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (m Model) View() string {
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

	view := tui.Screen(b.String(), m.footer(), m.height)
	if m.showHelp {
		return tui.KeyHelp(view, reviewKeys, m.width, m.height)
	}
	return view
}

// header is the banner plus one summary line: what this screen is on the left,
// what it holds on the right — the same left/right split every screen uses.
func (m Model) header() string {
	files := 0
	for _, n := range m.draft.Tree() {
		files += n.FileCount
	}
	right := human.Plural(len(m.rows), "folder", "folders") + " · " + human.Plural(files, "file", "files")
	if n := len(m.draft.Edits()); n > 0 {
		right += " · " + human.Plural(n, "edit", "edits")
	}
	return tui.Row(tui.DimText.Render("  Nothing moves until you copy. Edits are saved as you go."),
		tui.FaintTxt.Render(right), m.width) + "\n"
}

// rowView renders one tree line: guide + name, file count right-aligned.
func (m Model) rowView(i int, inRange bool) string {
	r := m.rows[i]

	cursor := "  "
	count := human.Plural(r.node.FileCount, "file", "files")

	if inRange || i == m.cursor {
		// Plain, no per-segment colour — a nested ANSI reset would cut the highlight short.
		if i == m.cursor {
			cursor = "❯ "
		}
		return tui.Selected.Render(tui.Row(cursor+r.guide+r.node.Name, count, m.width))
	}

	return tui.Row(cursor+tui.FaintTxt.Render(r.guide)+r.node.Name, tui.FaintTxt.Render(count), m.width)
}

// footer is everything below the tree: rename prompt, spinner, or status line
// plus key help. visibleRows measures its height.
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
			tui.KeyHint("↑↓", "pick a place"),
			tui.KeyHint("tab", "use match"),
			tui.KeyHint("ctrl+e", "search wider"),
			tui.KeyHint("enter", "rename"),
			tui.KeyHint("esc", "cancel"),
			tui.MoreKeys(),
		}, "   "), m.width))
	case m.previewing:
		b.WriteString(m.spin.View())
		b.WriteString(tui.DimText.Render(" Copying preview…"))
	default:
		if m.previewErr != nil {
			fmt.Fprintln(&b, tui.Bad.Render("Preview failed: ")+tui.Text.Render(m.previewErr.Error()))
		}
		if m.visualMode {
			fmt.Fprintln(&b, tui.Title.Render("-- SELECT --")+" "+tui.DimText.Render(fmt.Sprintf("%d folders", len(m.selectedRows()))))
		}
		switch {
		case m.statusMsg == "":
		case m.statusIsErr:
			fmt.Fprintln(&b, tui.Attn.Render("⚠ "+m.statusMsg))
		case m.statusUndo:
			fmt.Fprintln(&b, tui.OK.Render("✓ ")+tui.Text.Render(m.statusMsg)+tui.FaintTxt.Render(" · ")+tui.KeyHint("u", "undo"))
		default:
			fmt.Fprintln(&b, m.wrapDim(m.statusMsg))
		}
		b.WriteString(tui.Footer(m.keyHelp(), m.width))
	}
	return b.String()
}

// keyHelp is the footer: the reviewer's common keys, or a selection's actions; the rest are behind ?.
func (m Model) keyHelp() string {
	if m.visualMode {
		return strings.Join([]string{
			tui.KeyHint("m", "merge"),
			tui.KeyHint("d", "drop"),
			tui.KeyHint("D", "flatten"),
			tui.KeyHint("esc", "cancel"),
			tui.MoreKeys(),
		}, "   ")
	}
	return strings.Join([]string{
		tui.KeyHint("↑↓", "move"),
		tui.KeyHint("r", "rename"),
		tui.KeyHint("V", "select"),
		tui.KeyHint("esc", "done"),
		tui.MoreKeys(),
	}, "   ")
}

// reviewKeys is every key, behind ?.
var reviewKeys = []tui.KeyGroup{
	{Title: "Move", Keys: []tui.KeyLine{
		{Key: "↑ ↓", What: "one folder"},
		{Key: "n N", What: "next / previous at this level"},
		{Key: "p", What: "peek at a few of its photos"},
	}},
	{Title: "Change", Keys: []tui.KeyLine{
		{Key: "r", What: "rename"},
		{Key: "V", What: "select a run of folders, then:"},
		{Key: "  m", What: "merge them into one"},
		{Key: "  d", What: "drop: contents move up a level"},
		{Key: "  D", What: "flatten: everything below moves in"},
	}},
	{Title: "Go back", Keys: []tui.KeyLine{
		{Key: "u", What: "undo; again to keep going back"},
		{Key: "R", What: "start over from the plan"},
		{Key: "esc", What: "done; edits are kept for copy"},
	}},
	{Title: "Years and months stay fixed.", Keys: nil},
}
