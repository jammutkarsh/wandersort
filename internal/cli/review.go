// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/style"
)

func (a *App) newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review and correct the proposed folder structure",
		Long: `Walks the folder hierarchy proposed by the last scan so you can rename
directories the pipeline could not resolve confidently (for example an
unlocated event cluster) before anything is moved. Confirmed names are
remembered and suggested automatically on future scans.`,
		Example: `# Review interactively
wandersort review

# Accept every suggestion without prompting
wandersort review --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReview()
		},
	}

	cmd.Flags().Bool(flagYes, false, "Accept all suggestions without prompting")
	return cmd
}

func (a *App) runReview() error {
	if _, err := os.Stat(a.Config.AppDBPath); os.IsNotExist(err) {
		return fmt.Errorf("no database found — run 'wandersort scan' first")
	}

	l, err := lock.AcquireOutput(filepath.Dir(a.Config.LogFile))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer l.Unlock()

	ctx := context.Background()
	if err := a.InitAppDB(ctx); err != nil {
		return fmt.Errorf("app db: %w", err)
	}
	defer a.Close()

	sessionID, err := vfs.ProposalSession(ctx, a.AppDB)
	if err != nil {
		if errors.Is(err, vfs.ErrNoProposal) {
			return fmt.Errorf("no proposal to review — run 'wandersort scan' first")
		}
		return err
	}

	tree, err := vfs.BuildTree(ctx, sessionID, a.AppDB)
	if err != nil {
		return err
	}
	if len(tree) == 0 {
		return fmt.Errorf("no proposal to review — run 'wandersort scan' first")
	}

	if v.GetBool(flagYes) {
		for _, it := range collectReviewable(tree) {
			it.Name = it.Suggestions[0].Name
		}
	} else {
		m, err := tea.NewProgram(newReviewModel(tree), tea.WithOutput(os.Stderr), tea.WithAltScreen()).Run()
		if err != nil {
			return fmt.Errorf("review ui: %w", err)
		}
		rm := m.(reviewModel)
		if !rm.confirmed {
			return fmt.Errorf("review cancelled — nothing changed")
		}
		for _, r := range rm.rows {
			if name := strings.TrimSpace(r.newName); name != "" {
				r.node.Name = name
			}
		}
	}

	if err := vfs.Confirm(ctx, sessionID, a.AppDB, tree); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, style.Success.Render("Folder structure approved.")+
		style.Dim.Render(" Confirmed names will be suggested on future scans."))
	return nil
}

// collectReviewable returns pointers to every node carrying suggestions, in
// tree order, so edits through them mutate the tree passed to Confirm.
func collectReviewable(nodes []vfs.Node) []*vfs.Node {
	var out []*vfs.Node
	for i := range nodes {
		if len(nodes[i].Suggestions) > 0 {
			out = append(out, &nodes[i])
		}
		out = append(out, collectReviewable(nodes[i].Children)...)
	}
	return out
}

/* --- bubbletea model --- */

// reviewRow is one visible line of the proposed hierarchy: a tree node at its
// depth, plus the rename the TUI collects for it ("" = keep as proposed).
type reviewRow struct {
	node    *vfs.Node
	depth   int
	newName string
}

// flattenTree lays the whole tree out as indented rows so the reviewer can
// walk the proposed hierarchy top to bottom and rename any directory in place.
func flattenTree(nodes []vfs.Node, depth int) []*reviewRow {
	var rows []*reviewRow
	for i := range nodes {
		rows = append(rows, &reviewRow{node: &nodes[i], depth: depth})
		rows = append(rows, flattenTree(nodes[i].Children, depth+1)...)
	}
	return rows
}

type reviewModel struct {
	rows      []*reviewRow
	cursor    int
	offset    int // first visible row (scroll position)
	height    int
	editing   bool
	input     string
	confirmed bool
}

func newReviewModel(tree []vfs.Node) reviewModel {
	return reviewModel{rows: flattenTree(tree, 0)}
}

func (m reviewModel) Init() tea.Cmd { return nil }

// visibleRows is how many tree lines fit between the header and the help bar.
func (m reviewModel) visibleRows() int {
	return max(m.height-6, 1)
}

// scrollIntoView keeps the cursor inside the visible window.
func (m *reviewModel) scrollIntoView() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.visibleRows() {
		m.offset = m.cursor - m.visibleRows() + 1
	}
}

func (m reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.height = ws.Height
		m.scrollIntoView()
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.editing {
		switch key.Type {
		case tea.KeyEnter:
			m.rows[m.cursor].newName = m.input
			m.editing = false
		case tea.KeyEsc:
			m.editing = false
		case tea.KeyBackspace:
			if len(m.input) > 0 {
				r := []rune(m.input)
				m.input = string(r[:len(r)-1])
			}
		case tea.KeySpace:
			m.input += " "
		case tea.KeyRunes:
			m.input += string(key.Runes)
		}
		return m, nil
	}

	switch key.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case "enter":
		if r := m.rows[m.cursor]; len(r.node.Suggestions) > 0 {
			r.newName = r.node.Suggestions[0].Name
		}
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case "r":
		r := m.rows[m.cursor]
		m.input = r.newName
		if m.input == "" {
			m.input = r.node.Name
		}
		m.editing = true
	case "p":
		if s := m.rows[m.cursor].node.Samples; len(s) > 0 {
			openInViewer(s[0])
		}
	case "a":
		for _, r := range m.rows {
			if r.newName == "" && len(r.node.Suggestions) > 0 {
				r.newName = r.node.Suggestions[0].Name
			}
		}
	case "c":
		m.confirmed = true
		return m, tea.Quit
	}
	m.scrollIntoView()
	return m, nil
}

func (m reviewModel) View() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.Header.Render("Review proposed folders"))
	fmt.Fprintln(&b, style.Dim.Render("Walk the proposed hierarchy — rename any folder, accept suggestions on unresolved ones."))
	fmt.Fprintln(&b)

	end := min(m.offset+m.visibleRows(), len(m.rows))
	for i := m.offset; i < end; i++ {
		r := m.rows[i]
		cursor := "  "
		if i == m.cursor {
			cursor = style.Warn.Render("> ")
		}
		indent := strings.Repeat("  ", r.depth)
		branch := ""
		if r.depth > 0 {
			branch = style.Dim.Render("└ ")
		}
		name := r.node.Name
		if r.newName != "" && r.newName != r.node.Name {
			name = r.node.Name + " → " + style.Success.Render(r.newName)
		}
		info := fmt.Sprintf("%d files", r.node.FileCount)
		if len(r.node.Suggestions) > 0 {
			info += ", suggested: " + r.node.Suggestions[0].Name
		}
		fmt.Fprintf(&b, "%s%s%s%s  %s\n", cursor, indent, branch, name, style.Dim.Render("("+info+")"))
	}

	fmt.Fprintln(&b)
	if m.editing {
		fmt.Fprintf(&b, "%s%s█\n", style.Warn.Render("New name: "), m.input)
		fmt.Fprintln(&b, style.Dim.Render("[enter] apply  [esc] cancel"))
	} else {
		fmt.Fprintln(&b, style.Dim.Render("[↑/↓] move  [enter] accept suggestion  [r] rename  [p] peek  [a] accept all  [c] confirm  [q] quit"))
	}
	return b.String()
}

// openInViewer opens a file in the OS default viewer, best-effort.
func openInViewer(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}
