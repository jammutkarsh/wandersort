// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// Options is everything the review TUI needs. Resolver may be nil — rename
// autocomplete degrades gracefully without it.
type Options struct {
	DB        *db.DB
	Tree      []vfs.Node
	Resolver  *location.Resolver
	Log       logger.Logger
	OutputDir string // for the post-approve free-space check
	// Load, when Tree is empty, lets Run show the TUI immediately behind a
	// loading spinner instead of blocking the terminal on the location
	// resolver, --rebuild's vfs.Propose, and vfs.BuildTree before the program
	// ever appears.
	Load func(ctx context.Context) ([]vfs.Node, *location.Resolver, error)
	// Rebuild re-proposes the whole hierarchy from the caller's current
	// settings and returns the new tree, behind [R]. Nil hides the key — the
	// review can't do this itself, it has neither the settings nor the phase.
	Rebuild func(ctx context.Context) ([]vfs.Node, error)
	// SettingsChanged opens the review with the rebuild question already up —
	// the caller compared the config stamp before building the screen.
	SettingsChanged bool
}

// SettingsChangedMsg tells an already-open review that the settings moved
// under it. The stamp check only runs when a review screen is built, so
// without this a change made while the review is on screen — which is exactly
// what the unified shell makes easy — would never be noticed.
type SettingsChangedMsg struct{}

// Run drives the standalone full-screen review to completion and writes the
// approved plan. Returns an error if the reviewer quit without saving.
func Run(ctx context.Context, o Options) error {
	var first tea.Model
	if o.Load != nil && len(o.Tree) == 0 {
		first = newLoadingScreen(ctx, o)
	} else {
		first = newModel(o.Tree, ctx, o.DB, o.Resolver, o.Log).withHost(o)
	}
	m, err := tea.NewProgram(tui.NewShell(first), tea.WithOutput(os.Stderr), tea.WithAltScreen()).Run()
	if err != nil {
		return fmt.Errorf("review ui: %w", err)
	}
	cur := m.(tui.Shell).Current()
	if ls, ok := cur.(loadingScreen); ok {
		if ls.err != nil {
			return ls.err
		}
		return fmt.Errorf("review cancelled — nothing changed")
	}
	rm := cur.(Model)
	defer cleanupPreviewDirs(rm.previewDirs)
	if !rm.confirmed {
		return fmt.Errorf("review cancelled — nothing changed")
	}
	if err := vfs.Confirm(ctx, o.DB, rm.tree); err != nil {
		return err
	}
	// review is the last look before a plan is approved — finding out mid-move
	// that the output volume is too small is far worse than being told here
	volume.CheckOutputSpace(ctx, o.DB, o.Log, o.OutputDir)
	return nil
}

// ConfirmAll writes the proposed hierarchy as-is, without showing a TUI
// (`wandersort review --yes`). Suggestions are what the reviewer would rename
// a folder *to* — taking them unattended is a decision nobody made.
func ConfirmAll(ctx context.Context, o Options) error {
	if err := vfs.Confirm(ctx, o.DB, o.Tree); err != nil {
		return err
	}
	volume.CheckOutputSpace(ctx, o.DB, o.Log, o.OutputDir)
	return nil
}

// Screen returns the review as an app-shell screen, so a scan can swap into it
// inside its own bubbletea program. Pass the model the shell leaves behind to
// Outcome.
func Screen(ctx context.Context, o Options) tea.Model {
	m := newModel(o.Tree, ctx, o.DB, o.Resolver, o.Log).withHost(o)
	m.embedded = true
	return screen{
		inner:     m,
		ctx:       ctx,
		db:        o.DB,
		log:       o.Log,
		outputDir: o.OutputDir,
	}
}

// Outcome reports how an embedded review ended. ok is false when m is not a
// review screen at all.
func Outcome(m tea.Model) (confirmed bool, err error, ok bool) {
	s, ok := m.(screen)
	if !ok {
		return false, nil, false
	}
	return s.confirmed, s.finalErr, true
}

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
	tree      []vfs.Node
	rows      []*reviewRow
	cursor    int
	offset    int // first visible row (scroll position)
	height    int
	width     int
	editing   bool
	input     string
	confirmed bool

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
	quitWarned   bool // [ctrl+c] with pending edits warns once before discarding
	// embedded runs the model inside the app-shell (scan → review swap): it
	// sets done instead of tea.Quit, and the shell wrapper finalizes.
	embedded bool
	done     bool
	// [R] re-proposes the whole hierarchy from the caller's current settings.
	// Both a settings change and the key itself raise askRebuild — a
	// full-screen yes/no the reviewer has to answer, not a status line: the
	// plan on screen no longer matches the settings, and a line above the key
	// bar is exactly what nobody reads. It is also the only warning a rebuild
	// needs (it discards every edit), which is why there is no press-twice
	// dance on top of it.
	rebuild       func(ctx context.Context) ([]vfs.Node, error)
	askRebuild    bool
	rebuildChoice bool // which button the modal has under the cursor
	approvedFiles int  // what a rebuild would re-propose; named in the modal
	rebuilding    bool
	// undo holds one whole-tree snapshot per structural edit, so [u] walks all
	// the way back — a reshaped tree can't be restored from per-row names.
	undo        []undoStep
	statusMsg   string
	statusIsErr bool // rejection, not confirmation: rendered in a warning colour

	// async preview copy ([p])
	previewing  bool
	previewErr  error
	previewDirs map[string]string // file-signature → temp dir, removed on exit
	spin        spinner.Model
}

func newModel(tree []vfs.Node, ctx context.Context, database *db.DB, resolver *location.Resolver, log logger.Logger) Model {
	// same spinner the scan and install screens run
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(tui.Primary)
	m := Model{
		spin:        sp,
		suggCursor:  -1,
		tree:        tree,
		rows:        buildRows(tree),
		ctx:         ctx,
		db:          database,
		resolver:    resolver,
		log:         log,
		previewDirs: map[string]string{},
	}
	// user_labels only changes on Confirm, after this TUI exits, so the set is
	// fixed for the session — load it once instead of querying per keystroke.
	m.labels = vfs.Labels(ctx, database, log)
	return m
}

// withHost applies the parts of Options only the caller can answer — how to
// rebuild, and whether the settings already moved. Separate from newModel so
// the loading screen and the two entry points share one constructor.
func (m Model) withHost(o Options) Model {
	m.rebuild = o.Rebuild
	if o.SettingsChanged {
		m.raiseRebuildAsk()
	}
	return m
}

// raiseRebuildAsk puts the rebuild question up. The approved count is read
// here, once, rather than per frame — the modal names it because that is the
// work a rebuild throws away.
func (m *Model) raiseRebuildAsk() {
	if m.rebuild == nil { // the host can't rebuild; asking would go nowhere
		return
	}
	m.askRebuild = true
	m.rebuildChoice = true
	m.approvedFiles, _ = vfs.ApprovedCount(m.ctx, m.db)
}

// rebuiltMsg carries [R]'s new tree back from the vfs phase.
type rebuiltMsg struct {
	tree []vfs.Node
	err  error
}

// rebuilt swaps in the re-proposed hierarchy. Everything derived from the old
// tree goes with it: the undo stack can't describe edits to folders that no
// longer exist, and the cursor's row is gone.
func (m Model) rebuilt(msg rebuiltMsg) Model {
	m.rebuilding = false
	if msg.err != nil {
		m.statusMsg, m.statusIsErr = "rebuild failed: "+msg.err.Error(), true
		return m
	}
	// An empty tree would leave every key that reads the cursor row with
	// nothing to read; keeping the old one is also the more useful answer.
	if len(msg.tree) == 0 {
		m.statusMsg, m.statusIsErr = "rebuild proposed no folders — keeping the current plan", true
		return m
	}
	m.tree = msg.tree
	m.undo = nil
	m.cursor, m.offset = 0, 0
	m.visualMode = false
	m.reflow()
	m.statusMsg, m.statusIsErr = "folders re-proposed with your current settings", false
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
	vfs.SortTree(m.tree)
	m.rows = buildRows(m.tree)
	m.cursor = min(m.cursor, len(m.rows)-1) // the tree may have shrunk
}

// hasEdits reports whether quitting would lose an edit. Every edit — rename
// included — snapshots the tree first, so the undo stack is the whole answer.
func (m Model) hasEdits() bool { return len(m.undo) > 0 }

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
func (m *Model) focusNode(id string) {
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
