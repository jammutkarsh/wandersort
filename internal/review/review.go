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
	// Rebuild re-proposes the hierarchy from the caller's current settings and
	// returns the new tree, behind [R]. Nil hides the key — the review can't do
	// this itself, it has neither the settings nor the phase. seg is the slice
	// the screen is reviewing: re-proposing is library-wide, but the tree handed
	// back has to stay scoped to it, or a reset inside one time slice replaces
	// it with the whole library.
	Rebuild func(ctx context.Context, seg *vfs.Segment) ([]vfs.Node, error)
	// SettingsChanged opens the review with the rebuild question already up —
	// the caller compared the config stamp before building the screen.
	SettingsChanged bool
	// SegmentMonths is the reviewer's time-slice size from the settings
	// (0 = let vfs.Segments pick from the library's span).
	SegmentMonths int
	// Segment is the slice this screen reviews — set by the picker when it
	// opens one, nil for a whole-library review. It scopes what Confirm
	// approves, and names itself in the header.
	Segment *vfs.Segment
}

// SettingsChangedMsg tells an already-open review that the settings moved
// under it. The stamp check only runs when a review screen is built, so
// without this a change made while the review is on screen — which is exactly
// what the unified shell makes easy — would never be noticed.
type SettingsChangedMsg struct{}

// ConfirmAll writes the proposed hierarchy as-is, without showing a TUI
// (`wandersort review --yes`). Suggestions are what the reviewer would rename
// a folder *to* — taking them unattended is a decision nobody made.
func ConfirmAll(ctx context.Context, o Options) error {
	// no segment: --yes is a decision about the whole library
	if err := vfs.Confirm(ctx, o.DB, o.Tree, nil); err != nil {
		return err
	}
	volume.CheckOutputSpace(ctx, o.DB, o.Log, o.OutputDir)
	return nil
}

// Screen returns the review as an app-shell screen — the only interactive
// entry point. Every full-screen command is the same shell opened on a
// different tab, so a review is always hosted, never its own program. Pass the
// model the shell leaves behind to Outcome.
func Screen(ctx context.Context, o Options) tea.Model {
	if segs := segmentsFor(ctx, o); segs != nil {
		return newPicker(ctx, o, segs)
	}
	return newSegmentScreen(ctx, o, nil)
}

// segmentsFor asks whether this proposal is worth slicing up. A failure is
// not one: the worst it costs is a whole-library review, which is what every
// review was until segments existed.
//
// ponytail: the caller has already built the whole tree by the time this says
// yes, and that tree is then thrown away for the per-segment ones. One extra
// BuildTree; have the caller hand over a tree-builder instead of a tree if it
// ever shows up in a profile.
func segmentsFor(ctx context.Context, o Options) []vfs.Segment {
	segs, err := vfs.Segments(ctx, o.DB, o.SegmentMonths)
	if err != nil && o.Log != nil {
		o.Log.Warn("Could not split the proposal into time slices, reviewing all of it", "error", err)
	}
	return segs
}

// newSegmentScreen is the tree review as an in-program screen: it confirms
// what it was given (scoped to o.Segment) and then goes back to the picker it
// was opened from, or hands control to its caller when there isn't one.
func newSegmentScreen(ctx context.Context, o Options, host *pickerModel) tea.Model {
	m := newModel(o.Tree, ctx, o.DB, o.Resolver, o.Log).withHost(o)
	m.embedded = true
	m.hosted = host != nil
	return screen{
		inner:     m,
		ctx:       ctx,
		db:        o.DB,
		log:       o.Log,
		outputDir: o.OutputDir,
		seg:       o.Segment,
		host:      host,
	}
}

// Outcome reports how an embedded review ended. ok is false when m is not a
// review screen at all.
func Outcome(m tea.Model) (confirmed bool, err error, ok bool) {
	switch s := m.(type) {
	case screen:
		return s.confirmed, s.finalErr, true
	case pickerModel:
		// a segmented review saves as it goes: "confirmed" is "at least one
		// slice was signed off", not one final write
		return s.saved > 0, nil, true
	}
	return false, nil, false
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
	// segLabel names the time slice this tree is, when the review is
	// segmented — the header is the only thing that says which one is open.
	segLabel string
	// seg is that slice as the database knows it, so [R] can re-propose without
	// widening this screen to the whole library.
	seg *vfs.Segment
	// hosted is "a segment picker is underneath this screen": discarding via
	// [esc] goes back to it instead of ending the review.
	hosted bool
	// back is a discard's answer when hosted — done without confirming, but
	// not a quit.
	back bool
	// askExit is [esc]'s question — save this plan, or throw the edits away —
	// raised on every [esc] rather than assuming either answer. A second
	// [esc] inside it forcefully discards; ctrl+c does too.
	askExit    bool
	exitChoice bool // true = Save, false = Discard; which button is under the cursor

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
	// [R] resets the plan: re-proposes the whole hierarchy from the caller's
	// current settings. Both a settings change and the key itself raise
	// askRebuild — a full-screen yes/no the reviewer has to answer, not a
	// status line: the plan on screen no longer matches the settings, and a
	// line above the key bar is exactly what nobody reads. It is also the only
	// warning a reset needs (it discards every edit), which is why there is no
	// press-twice dance on top of it.
	rebuild       func(ctx context.Context, seg *vfs.Segment) ([]vfs.Node, error)
	askRebuild    bool
	rebuildChoice bool // which button the modal has under the cursor
	// askedBySettings is why the question is up — a settings change, rather
	// than the reviewer pressing [R] to start over. Only the wording differs.
	askedBySettings bool
	rebuilding      bool
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
// a whole-library review and a segment's share one constructor.
func (m Model) withHost(o Options) Model {
	m.rebuild = o.Rebuild
	m.seg = o.Segment
	if o.Segment != nil {
		m.segLabel = o.Segment.Label
	}
	if o.SettingsChanged {
		m.raiseRebuildAsk(true)
	}
	return m
}

// raiseRebuildAsk puts the reset question up. settingsMoved only picks the
// wording — the answer does the same thing either way.
func (m *Model) raiseRebuildAsk(settingsMoved bool) {
	if m.rebuild == nil { // the host can't re-propose; asking would go nowhere
		return
	}
	m.askRebuild = true
	m.rebuildChoice = true
	m.askedBySettings = settingsMoved
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
		m.statusMsg, m.statusIsErr = "reset failed: "+msg.err.Error(), true
		return m
	}
	// An empty tree would leave every key that reads the cursor row with
	// nothing to read; keeping the old one is also the more useful answer.
	if len(msg.tree) == 0 {
		m.statusMsg, m.statusIsErr = "reset proposed no folders — keeping the current plan", true
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
