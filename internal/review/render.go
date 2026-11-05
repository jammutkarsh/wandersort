package review

import (
	"fmt"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/human"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (m Model) View() string {
	if m.ask != nil {
		return m.ask.View(m.width, m.height)
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
		line := m.rowView(i, selLo != -1 && i >= selLo && i <= selHi)
		if !m.editing && !m.visualMode {
			line = tui.Clickable(line, tui.MoveKeys(m.cursor, i)...)
		}
		b.WriteString("\n" + line)
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
	return "\n" + tui.Row(tui.DimText.Render("Nothing moves until you copy. Edits are saved as you go."),
		tui.FaintTxt.Render(right), m.width) + "\n"
}

// rowView renders one tree line: guide + name, file count right-aligned.
func (m Model) rowView(i int, inRange bool) string {
	r := m.rows[i]

	cursor := "  "
	count := human.Plural(r.node.FileCount, "file", "files")

	// a folder the copy creates is marked, not only coloured
	mark := ""
	if r.isNew {
		mark = newMark
	}
	if inRange || i == m.cursor {
		// the guide stays dim and the name is the Pill, the same pick as everywhere
		return tui.Row(cursor+tui.FaintTxt.Render(strings.TrimSuffix(r.guide, " "))+tui.Pill(mark+r.node.Name), tui.FaintTxt.Render(count), m.width)
	}

	name := r.node.Name
	if r.isNew {
		name = tui.NewFolder.Render(mark + name)
	}
	return tui.Row(cursor+tui.FaintTxt.Render(r.guide)+name, tui.FaintTxt.Render(count), m.width)
}

// footer is everything below the tree: rename prompt, spinner, or status line
// plus key help. visibleRows measures its height.
func (m Model) footer() string {
	var b strings.Builder
	switch {
	case m.editing:
		fmt.Fprintf(&b, "%s%s%s█\n",
			tui.DimText.Render("Rename "+m.rows[m.cursor].node.Name+" to "),
			tui.Prompt(), tui.Text.Render(m.input))
		for i, s := range m.suggestions {
			// same shape as the wizard's completion list: dim rows, the arrowed-onto
			// one highlighted, and the top match — what [tab] fills in — says so
			line := "    "
			if i == m.suggCursor {
				line += tui.Pill(s.Label)
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
			fmt.Fprintln(&b, tui.Row(tui.Clickable(line, append(tui.MoveKeys(m.suggCursor, i), "enter")...), "", m.width))
		}
		b.WriteString(tui.Footer(strings.Join([]string{
			tui.KeyHint("↑↓", "pick a place"),
			tui.KeyHint("ctrl+e", "more places"),
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
			fmt.Fprintln(&b, tui.Title.Render("Selecting "+human.Plural(len(m.selectedRows()), "folder", "folders"))+" "+tui.DimText.Render("· ↑↓ to pick more"))
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
			tui.KeyHint("m", "combine"),
			tui.KeyHint("x", "remove folder"),
			tui.KeyHint("f", "gather"),
			tui.KeyHint("esc", "cancel"),
			tui.MoreKeys(),
		}, "   ")
	}
	return strings.Join([]string{
		tui.KeyHint("↑↓", "move"),
		tui.KeyHint("r", "rename"),
		tui.KeyHint("space", "select"),
		tui.KeyHint("esc", "done"),
		tui.MoreKeys(),
	}, "   ")
}

// plannedFiles is how many files the tree holds, none of them copied yet.
func (m Model) plannedFiles() int {
	files := 0
	for _, n := range m.draft.Tree() {
		files += n.FileCount
	}
	return files
}

// newMark is in front of every folder the copy creates.
const newMark = "+ "

// reviewKeys is every key, behind ?.
var reviewKeys = []tui.KeyGroup{
	{Title: "Move", Keys: []tui.KeyLine{
		{Key: "↑ ↓", What: "one folder"},
		{Key: "← →", What: "previous / next at this level"},
		{Key: "p", What: "peek at a few of its photos"},
	}},
	{Title: "Change", Keys: []tui.KeyLine{
		{Key: "r", What: "rename"},
		{Key: "space", What: "select folders (or shift+↑↓), then:"},
		{Key: "  m", What: "combine them into one folder"},
		{Key: "  x", What: "remove folder; what's inside moves up a level"},
		{Key: "  f", What: "gather: everything inside them moves into this folder"},
	}},
	{Title: "Go back", Keys: []tui.KeyLine{
		{Key: "u", What: "undo; again to keep going back"},
		{Key: "ctrl+u", What: "undo every edit"},
		{Key: "ctrl+r", What: "start over: take the files not copied yet off the list"},
		{Key: "esc", What: "done; edits are kept for copy"},
	}},
	{Title: "Folders", Keys: []tui.KeyLine{
		{Key: tui.NewFolder.Render(newMark + "blue"), What: "new: the copy makes it"},
		{Key: "others", What: "already in your library"},
	}},
	{Title: "Years and months stay fixed.", Keys: nil},
}
