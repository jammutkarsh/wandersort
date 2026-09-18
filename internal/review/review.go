// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
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
}

// ConfirmAll writes the proposed hierarchy as-is, without showing a TUI
// (`wandersort review --yes`). Suggestions are what the reviewer would rename
// a folder *to* — taking them unattended is a decision nobody made.
func ConfirmAll(ctx context.Context, o Options) error {
	if err := vfs.Confirm(ctx, o.DB, o.Tree); err != nil {
		return err
	}
	volume.CheckOutputSpace(ctx, o.DB, o.Log, o.OutputDir)
	if err := CleanPreviews(); err != nil {
		o.Log.Warn("could not remove the preview copies", "error", err)
	}
	return nil
}

// Screen returns the review as an app-shell screen — the only interactive
// entry point. Every full-screen command is the same shell opened on a
// different tab, so a review is always hosted, never its own program. Pass the
// model the shell leaves behind to Outcome.
func Screen(ctx context.Context, o Options) tea.Model {
	m := newModel(o.Tree, ctx, o.DB, o.Resolver, o.Log, o.OutputDir)
	m.embedded = true
	return screen{
		inner:     m,
		ctx:       ctx,
		db:        o.DB,
		log:       o.Log,
		outputDir: o.OutputDir,
	}
}

// Result reports how an embedded review ended. Confirmed/Err answer "was
// [esc] -> Save pressed, and did it work" — but [x]/[X] can transfer files to
// the output at any point in the session regardless of whether the review is
// ever explicitly saved (the review can even end on its own once a transfer
// empties the tree — see reset's postTransferSync path), so TransferDone/
// TransferFailed carry that separately: a caller reporting only Confirmed
// would say "nothing changed" over a session that copied or moved real files.
type Result struct {
	Confirmed                    bool
	Err                          error
	TransferDone, TransferFailed int
}

// Outcome reports how an embedded review ended. ok is false when m is not a
// review screen at all.
func Outcome(m tea.Model) (Result, bool) {
	s, ok := m.(screen)
	if !ok {
		return Result{}, false
	}
	return Result{
		Confirmed:      s.confirmed,
		Err:            s.finalErr,
		TransferDone:   s.inner.transferDone,
		TransferFailed: s.inner.transferFailed,
	}, true
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
	// askExit is [esc]'s question — save this plan, or throw the edits away —
	// raised on every [esc] rather than assuming either answer. A second
	// [esc] inside it forcefully discards; ctrl+c does too.
	askExit    bool
	exitChoice bool // true = Save, false = Discard; which button is under the cursor

	ctx       context.Context
	db        *db.DB
	resolver  *location.Resolver
	log       logger.Logger
	outputDir string // for the pre-transfer free-space check

	// [x]/[X] copy or move every APPROVED file to the output right now. Not
	// scoped to a selection — see transfer.go.
	askMove      bool
	moveChoice   bool // which button the move-ask modal has under the cursor
	transferring bool
	transferMode execute.Mode
	// prog is the last progress report drawn, progCh the channel execute's
	// OnProgress feeds it down. bar renders it — a transfer is the one thing
	// here that runs for minutes over gigabytes, so it gets a bar, not a bare
	// spinner.
	prog   transferProgressMsg
	progCh chan transferProgressMsg
	bytes  int64 // running total, since OnProgress reports one file's size
	bar    progress.Model
	// postTransferSync marks the reload a finished transfer triggers (via the
	// same resetCmd/resetMsg [R] uses) — nothing was "discarded" to get there
	// (the plan was already saved before the transfer started), so reset must
	// leave the transfer's own status line alone instead of overwriting it.
	postTransferSync bool
	// transferDone/transferFailed accumulate every [x]/[X] this session, so
	// Outcome can report what actually reached disk even when the review ends
	// without an explicit [esc] -> Save — a transfer can close the review on
	// its own (see reset's postTransferSync case) or race an [esc] ->
	// Discard/ctrl+c that lands before its own reload does.
	transferDone, transferFailed int

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
	// [R] resets the plan: discards every edit and reloads the still-proposed
	// rows straight from the database — no confirmation, since nothing it does
	// is a surprise (the same discard [u] already does one step at a time).
	// resetting gates the spinner while that query runs.
	resetting bool
	// undo holds one whole-tree snapshot per structural edit, so [u] walks all
	// the way back — a reshaped tree can't be restored from per-row names.
	undo        []undoStep
	statusMsg   string
	statusIsErr bool // rejection, not confirmation: rendered in a warning colour

	// async preview copy ([p])
	previewing bool
	previewErr error
	spin       spinner.Model
}

func newModel(tree []vfs.Node, ctx context.Context, database *db.DB, resolver *location.Resolver, log logger.Logger, outputDir string) Model {
	// same spinner the scan and install screens run
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(tui.Primary)
	m := Model{
		spin:       sp,
		bar:        progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
		suggCursor: -1,
		tree:       tree,
		rows:       buildRows(tree),
		ctx:        ctx,
		db:         database,
		resolver:   resolver,
		log:        log,
		outputDir:  outputDir,
	}
	// user_labels only changes on Confirm, after this TUI exits, so the set is
	// fixed for the session — load it once instead of querying per keystroke.
	m.labels = vfs.Labels(ctx, database, log)
	return m
}

// resetMsg carries [R]'s reload back from the database.
type resetMsg struct {
	tree []vfs.Node
	err  error
}

// resetCmd re-reads the still-proposed rows, discarding every in-memory edit.
// Nothing on disk changes — a reset only ever throws away what was never
// saved; already-approved rows aren't part of this query at all, so they're
// untouched by construction.
func resetCmd(ctx context.Context, database *db.DB) tea.Cmd {
	return func() tea.Msg {
		tree, err := vfs.BuildTree(ctx, database)
		return resetMsg{tree: tree, err: err}
	}
}

// reset swaps in the freshly read tree. Everything derived from the old one
// goes with it: the undo stack can't describe edits to folders that may no
// longer be at the same rows, and the cursor's row is gone.
func (m Model) reset(msg resetMsg) Model {
	m.resetting = false
	quiet := m.postTransferSync
	m.postTransferSync = false
	if msg.err != nil {
		m.statusMsg, m.statusIsErr = "reset failed: "+msg.err.Error(), true
		return m
	}
	// Nothing left proposed here (every row in scope is already saved) leaves
	// every key that reads the cursor row with nothing to read; keeping the
	// old one is also the more useful answer.
	if len(msg.tree) == 0 {
		if quiet {
			// The transfer just cleared out everything reviewable — there is
			// nothing left to keep this screen open for. Staying on the
			// pre-transfer tree left the reviewer's very next [esc] -> Save
			// calling Confirm over zero reviewable rows, which fails with
			// "proposal was replaced by a newer scan" — true of the query,
			// false and alarming right after a clean transfer.
			m.done = true
			return m
		}
		m.statusMsg, m.statusIsErr = "nothing left to reset — this plan is already saved", true
		return m
	}
	m.tree = msg.tree
	m.undo = nil
	m.cursor, m.offset = 0, 0
	m.visualMode = false
	m.reflow()
	if !quiet {
		m.statusMsg, m.statusIsErr = "unsaved edits discarded", false
	}
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
