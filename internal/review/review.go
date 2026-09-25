// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

// Options is everything the review TUI needs. Resolver may be nil — rename
// autocomplete degrades gracefully without it. Draft is the plan with the
// reviewer's edits so far (vfs.OpenDraft); every edit made on screen goes
// through it and into its journal.
type Options struct {
	DB       *db.DB
	Draft    *vfs.Draft
	Resolver *location.Resolver
	Log      logger.Logger
}

// Screen returns the review as an app-shell screen — the only interactive
// entry point. Every full-screen command is the same shell opened on a
// different tab, so a review is always hosted, never its own program. It
// writes nothing but the draft file: leaving hands back to the shell with
// tui.Switch(nil), and `wandersort execute` applies the edits.
func Screen(ctx context.Context, o Options) tui.Tab {
	return newModel(o.Draft, ctx, o.DB, o.Resolver, o.Log)
}

// Busy is never true for the review: it reads a plan and writes a journal,
// with nothing in flight the container has to wait for.
func (m Model) Busy() bool { return false }

/* --- bubbletea model --- */

// reviewRow is one visible line of the proposed hierarchy: a tree node at its
// depth. Renames are written straight onto the node, so a row holds no name
// state of its own. parent identifies true siblings for the merge command —
// nil for top-level (Year) rows, which are siblings of each other too.
type reviewRow struct {
	node   *vfs.Node
	parent *vfs.Node
	depth  int
	// guide is the drawn tree prefix ("│  ├─ "), precomputed here because a row
	// can't tell whether it's a last child from its depth alone.
	guide string
}

// flattenTree lays the whole tree out as rows top to bottom, so the reviewer
// can walk the proposed hierarchy and rename any directory in place. It does
// not fill in guide — see buildRows.
func flattenTree(nodes []vfs.Node, depth int, parent *vfs.Node) []*reviewRow {
	var rows []*reviewRow
	for i := range nodes {
		rows = append(rows, &reviewRow{node: &nodes[i], parent: parent, depth: depth})
		rows = append(rows, flattenTree(nodes[i].Children, depth+1, &nodes[i])...)
	}
	return rows
}

// buildRows flattens tree and fills in each row's box-drawing guide
// ("│  ├─ ") via tui.Guides — a row can't tell whether it's a last child from
// its own depth alone, so that needs every row to exist first.
func buildRows(tree []vfs.Node) []*reviewRow {
	rows := flattenTree(tree, 0, nil)
	depths := make([]int, len(rows))
	for i, r := range rows {
		depths[i] = r.depth
	}
	guides := tui.Guides(depths)
	for i, r := range rows {
		r.guide = guides[i]
	}
	return rows
}

type Model struct {
	tree    []vfs.Node
	rows    []*reviewRow
	cursor  int
	offset  int // first visible row (scroll position)
	height  int
	width   int
	editing bool
	input   string

	ctx      context.Context
	db       *db.DB
	resolver *location.Resolver
	log      logger.Logger

	// Rename autocomplete. Both sources are fetched up front and filtered in
	// memory per keystroke, so typing never hits the DB.
	suggestions []location.Suggestion
	suggCursor  int                  // ↑/↓-picked suggestion; -1 = none picked
	geoCands    []location.Candidate // refetched only by [r] and ctrl+e
	labels      []string             // confirmed names, loaded once at startup
	radiusDelta float64              // live search width; ctrl+e widens it

	// Structural edits: [V] selects, [m] merges, [d]/[D] remove nesting.
	visualMode   bool
	visualAnchor int
	showHelp     bool // [?] — full-screen key reference; any key closes it
	// draft owns the plan as proposed, the edit journal and the tree they
	// make; tree above is always draft.Tree(), which is what lets [u] drop a
	// line and [R] drop them all.
	draft       *vfs.Draft
	statusMsg   string
	statusIsErr bool // rejection, not confirmation: rendered in a warning colour

	// async preview copy ([p])
	previewing bool
	previewErr error
	spin       spinner.Model
}

func newModel(draft *vfs.Draft, ctx context.Context, database *db.DB, resolver *location.Resolver, log logger.Logger) Model {
	// same spinner the scan and install screens run
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(tui.Primary)
	m := Model{
		spin:       sp,
		suggCursor: -1,
		draft:      draft,
		tree:       draft.Tree(),
		ctx:        ctx,
		db:         database,
		resolver:   resolver,
		log:        log,
	}
	m.rows = buildRows(m.tree)
	// user_labels only changes when execute applies a plan, never while this
	// screen is up, so load it once instead of querying per keystroke.
	m.labels = vfs.Labels(ctx, database, log)
	return m
}

// reset is [R]: throw the draft away and show the plan as proposed. The
// database is untouched — it never held the edits.
func (m Model) reset() Model {
	if err := m.draft.Reset(); err != nil {
		m.statusMsg, m.statusIsErr = err.Error(), true
		return m
	}
	m.cursor, m.offset = 0, 0
	m.visualMode = false
	m.reflow()
	m.statusMsg, m.statusIsErr = "edits discarded — back to the plan as proposed", false
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// visibleRows is how many tree lines fit between the header and the footer.
// Both are measured, never assumed to be a fixed height — the key help wraps
// on a narrow terminal and the header carries a status line.
func (m Model) visibleRows() int {
	return max(m.height-lipgloss.Height(m.header())-lipgloss.Height(m.footer()), 1)
}

// wrapDim renders dimmed text word-wrapped to the terminal width.
func (m Model) wrapDim(s string) string {
	// no WindowSizeMsg yet: render unwrapped rather than to nothing
	if m.width <= 0 {
		return tui.DimText.Render(s)
	}
	return tui.DimText.Width(m.width).Render(s)
}

// reflow rebuilds the row list after a tree edit (rename, merge, drop, undo).
func (m *Model) reflow() {
	m.tree = m.draft.Tree()
	m.rows = buildRows(m.tree)
	m.cursor = min(m.cursor, len(m.rows)-1) // the tree may have shrunk
}

// jumpSameDepth moves the cursor to the next ([n]) or previous ([N]) row at
// the cursor's own depth, so a deep tree is walkable without scrolling through
// every folder's contents.
func (m *Model) jumpSameDepth(step int) {
	depth := m.rows[m.cursor].depth
	// crosses into other branches by design: that is what lets [V][n][n] select
	// one level across several months. Stops at the ends, never wraps.
	for i := m.cursor + step; i >= 0 && i < len(m.rows); i += step {
		if m.rows[i].depth == depth {
			m.cursor = i
			m.statusMsg, m.statusIsErr = "", false
			return
		}
	}
	m.statusMsg, m.statusIsErr = "no more folders at this level", true
}

// focusNode puts the cursor on a node by ID, so an edit that moves a folder
// leaves the reviewer looking at where it went rather than at whatever row
// happens to sit at the old index.
func (m *Model) focusNode(id int64) {
	for i, r := range m.rows {
		if r.node.ID == id {
			m.cursor = i
			return
		}
	}
}

// scrollIntoView keeps the cursor inside the visible window.
func (m *Model) scrollIntoView() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.visibleRows() {
		m.offset = m.cursor - m.visibleRows() + 1
	}
}
