// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

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
// parent identifies true siblings for the merge command — nil for top-level
// (Year) rows, which are siblings of each other too.
type reviewRow struct {
	node    *vfs.Node
	parent  *vfs.Node
	depth   int
	newName string
	// guide is the drawn tree prefix ("│  ├─ "), precomputed here because a row
	// can't tell whether it's a last child from its depth alone.
	guide string
}

// flattenTree lays the whole tree out as rows with box-drawing guides, so the
// reviewer can walk the proposed hierarchy top to bottom and rename any
// directory in place. prefix is the guide inherited from the parent row.
func flattenTree(nodes []vfs.Node, depth int, parent *vfs.Node, prefix string) []*reviewRow {
	var rows []*reviewRow
	for i := range nodes {
		branch, childPrefix := "├─ ", prefix+"│  "
		if i == len(nodes)-1 {
			branch, childPrefix = "└─ ", prefix+"   "
		}
		if depth == 0 { // top-level rows are roots, not siblings of one trunk
			branch, childPrefix = "", ""
		}
		rows = append(rows, &reviewRow{node: &nodes[i], parent: parent, depth: depth, guide: prefix + branch})
		rows = append(rows, flattenTree(nodes[i].Children, depth+1, &nodes[i], childPrefix)...)
	}
	return rows
}

// nameSuggestion is one rename candidate shown under the input.
type nameSuggestion struct {
	label  string // shown to the reviewer, e.g. "Springfield, Illinois"
	value  string // written as the folder name if picked
	detail string // right-hand hint, e.g. "~12km" or "used before"
}

// folderName turns a display name ("Springfield, Illinois") into a folder-safe
// one ("Springfield_Illinois") — the disambiguation comma is presentation only.
// folderName turns a picked display name (search results and the rename
// dropdown keep the comma — "Seoni, Himachal Pradesh" is how a reviewer tells
// two same-named places apart) into what actually becomes the folder: no
// space, no comma, same rule Confirm enforces on every node name.
func folderName(display string) string {
	return vfs.SanitizeSegment(display)
}

const maxPreviewBytes = 250 * 1024 * 1024

// defaultCandidateRadius is the initial search width for rename suggestions;
// ctrl+e widens it by the same step (see location.queryNearest's own ladder).
const defaultCandidateRadius = 0.09

// maxSuggestions caps the rename dropdown — it renders above the key help, so
// an unbounded list would push the tree off screen.
const maxSuggestions = 8

// geoCandidateFetch over-fetches nearby places: the list is filtered in memory
// as the reviewer types, so one fetch has to cover every prefix they might type.
const geoCandidateFetch = 64

// minGazetteerPrefix is how many characters must be typed before the per-
// keystroke gazetteer search runs; below it every query matches half the table.
const minGazetteerPrefix = 2

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
	suggestions []nameSuggestion
	geoCands    []location.Candidate // refetched only by [r] and ctrl+e
	labels      []string             // confirmed names, loaded once at startup
	radiusDelta float64              // live search width; ctrl+e widens it

	// Structural edits: [V] selects, [m] merges, [d]/[D] remove nesting.
	visualMode   bool
	visualAnchor int
	showHelp     bool // [?] — full-screen key reference; any key closes it
	quitWarned   bool // [q] with pending edits warns once before discarding
	// embedded runs the model inside the app-shell (scan → review swap): it
	// sets done instead of tea.Quit, and the shell wrapper finalizes.
	embedded bool
	done     bool
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
		tree:        tree,
		rows:        flattenTree(tree, 0, nil, ""),
		ctx:         ctx,
		db:          database,
		resolver:    resolver,
		log:         log,
		previewDirs: map[string]string{},
	}
	// user_labels only changes on Confirm, after this TUI exits, so the set is
	// fixed for the session — load it once instead of querying per keystroke.
	// Failure just means no "used before" suggestions.
	if database != nil {
		if err := database.SQL.SelectContext(ctx, &m.labels,
			`SELECT DISTINCT label FROM user_labels ORDER BY label`); err != nil && log != nil {
			log.Warn("Could not load confirmed labels for rename suggestions", "error", err)
		}
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

// reflow rebuilds the row list after a structural edit (merge, drop, undo).
func (m *Model) reflow() {
	vfs.SortTree(m.tree)
	// flattenTree allocates fresh rows, so pending renames have to be carried
	// across by node ID or a splice silently discards them
	pending := map[string]string{}
	for _, r := range m.rows {
		if r.newName != "" {
			pending[r.node.ID] = r.newName
		}
	}
	m.rows = flattenTree(m.tree, 0, nil, "")
	for _, r := range m.rows {
		if name, ok := pending[r.node.ID]; ok {
			r.newName = name
		}
	}
	m.cursor = min(m.cursor, len(m.rows)-1) // the tree may have shrunk
}

// undoStep is the tree as it stood before one structural edit, plus what that
// edit was, for the status line.
type undoStep struct {
	tree []vfs.Node
	edit string
}

// maxUndo caps how far back [u] walks. A step clones the folder tree only
// (never files), so each is cheap — this just bounds a long session's memory.
const maxUndo = 100

// snapshot records the tree before a structural edit so [u] can walk back to
// it. Called by every edit that reshapes the tree — merge, drop, flatten.
func (m *Model) snapshot(edit string) {
	m.undo = append(m.undo, undoStep{tree: vfs.CloneTree(m.tree), edit: edit})
	if len(m.undo) > maxUndo {
		m.undo = m.undo[len(m.undo)-maxUndo:]
	}
}

// hasEdits reports whether quitting would lose a rename or a structural edit.
// Derived, not tracked with a flag no edit path can forget to set.
func (m Model) hasEdits() bool {
	if len(m.undo) > 0 {
		return true
	}
	for _, r := range m.rows {
		if r.newName != "" {
			return true
		}
	}
	return false
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

// loadGeoCandidates caches nearby places for the current row. Called by [r] and
// ctrl+e only — refreshSuggestions filters this list in memory per keystroke.
func (m *Model) loadGeoCandidates() {
	m.geoCands = nil
	row := m.rows[m.cursor]
	if m.resolver == nil || row.node.Lat == nil || row.node.Lon == nil {
		return
	}
	if cands, err := m.resolver.Candidates(m.ctx, *row.node.Lat, *row.node.Lon, m.radiusDelta, geoCandidateFetch); err == nil {
		m.geoCands = cands
	}
}

// refreshSuggestions repopulates the rename dropdown from three ranked sources:
// nearby places, names confirmed in earlier reviews, then the gazetteer.
func (m *Model) refreshSuggestions() {
	m.suggestions = nil
	seen := map[string]bool{}
	typed := strings.TrimSpace(m.input)
	prefix := strings.ToLower(typed)

	add := func(label, detail string) bool {
		value := folderName(label)
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
		m.suggestions = append(m.suggestions, nameSuggestion{label: label, value: value, detail: detail})
		return len(m.suggestions) < maxSuggestions
	}

	// 1. nearby places, already fetched — FullName because six rows reading
	// "Springfield" are not a choice, and what is picked is what names the folder
	for _, c := range m.geoCands {
		name := c.FullName
		if name == "" {
			name = c.Name
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), prefix) &&
			!strings.HasPrefix(strings.ToLower(c.Name), prefix) {
			continue
		}
		if !add(name, fmt.Sprintf("~%.0fkm", c.DistKM)) {
			return
		}
	}

	if prefix == "" {
		return // the two typed-prefix sources below have nothing to match on
	}

	// 2. names the reviewer confirmed before
	for _, l := range m.labels {
		if !strings.HasPrefix(strings.ToLower(l), prefix) {
			continue
		}
		if !add(l, "used before") {
			return
		}
	}

	// 3. gazetteer prefix search, so typing a city works on a folder with no GPS
	// to seed geoCands. One indexed LIKE 'x%' per keystroke, from 2 chars on.
	if len(prefix) >= minGazetteerPrefix && m.resolver != nil {
		if matches, err := m.resolver.SearchByName(m.ctx, typed, maxSuggestions); err == nil {
			for _, pm := range matches {
				if !add(pm.FullName, "") {
					return
				}
			}
		}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height, m.width = msg.Height, msg.Width
		m.scrollIntoView()
		return m, nil
	case spinner.TickMsg:
		if !m.previewing {
			return m, nil // let the ticker die once the copy is done
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case previewDoneMsg:
		m.previewing = false
		m.previewErr = msg.err
		if msg.err == nil {
			m.previewDirs[msg.signature] = msg.dir
			openInViewer(msg.dir)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		switch key.Type {
		case tea.KeyEnter:
			m.rows[m.cursor].newName = folderName(m.input)
			m.editing = false
			m.suggestions = nil
		case tea.KeyEsc:
			m.editing = false
			m.suggestions = nil
		case tea.KeyBackspace:
			if len(m.input) > 0 {
				r := []rune(m.input)
				m.input = string(r[:len(r)-1])
			}
			m.refreshSuggestions()
		case tea.KeySpace:
			m.input += " "
			m.refreshSuggestions()
		case tea.KeyRunes:
			m.input += string(key.Runes)
			m.refreshSuggestions()
		case tea.KeyTab:
			if len(m.suggestions) > 0 {
				m.input = m.suggestions[0].value
				m.refreshSuggestions()
			}
		case tea.KeyCtrlE:
			m.radiusDelta += defaultCandidateRadius
			m.loadGeoCandidates() // the only key that re-queries mid-rename
			m.refreshSuggestions()
		}
		return m, nil
	}

	// the help screen swallows every key: whatever was pressed, come back
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	var cmd tea.Cmd
	// the "press q again" warning only stands for the very next keypress
	m.quitWarned = m.quitWarned && key.String() == "q"
	switch key.String() {
	case "ctrl+c":
		m.done = true
		if m.embedded {
			return m, nil
		}
		return m, tea.Quit
	case "q":
		// quitting throws away every rename and structural edit — make the
		// reviewer say so twice, and point at the key that saves instead
		if m.hasEdits() && !m.quitWarned {
			m.quitWarned = true
			m.statusMsg, m.statusIsErr = "unsaved changes — [c] saves and exits, press q again to discard them", true
			break
		}
		m.done = true
		if m.embedded {
			return m, nil
		}
		return m, tea.Quit
	case "esc":
		m.visualMode = false
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case "n":
		m.jumpSameDepth(1)
	case "N":
		m.jumpSameDepth(-1)
	case "enter":
		// Precedence: default name < location suggestion < user's own rename —
		// never clobber a manual rename the reviewer already typed, even if
		// they land back on this row and press enter again.
		if r := m.rows[m.cursor]; r.newName == "" && len(r.node.Suggestions) > 0 {
			r.newName = folderName(r.node.Suggestions[0].Name)
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
		m.radiusDelta = defaultCandidateRadius
		m.loadGeoCandidates()
		m.refreshSuggestions()
	case "p":
		if !m.previewing {
			m.previewing = true
			m.previewErr = nil
			node := m.rows[m.cursor].node
			// snapshot, not the live map: peekCmd's closure runs on another
			// goroutine and must never touch state Update also mutates
			cached := make(map[string]string, len(m.previewDirs))
			maps.Copy(cached, m.previewDirs)
			cmd = tea.Batch(peekCmd(m.ctx, m.db, node, cached), m.spin.Tick)
		}
	case "a":
		for _, r := range m.rows {
			if r.newName == "" && len(r.node.Suggestions) > 0 {
				r.newName = folderName(r.node.Suggestions[0].Name)
			}
		}
	case "V":
		if m.visualMode {
			m.visualMode = false
		} else {
			m.visualMode = true
			m.visualAnchor = m.cursor
		}
		m.statusMsg, m.statusIsErr = "", false
	case "m":
		m.mergeSelection()
	case "d":
		m.dropFolders(m.selectedRows())
	case "D":
		m.flattenFolders(m.selectedRows())
	case "?":
		m.showHelp = true
	case "u":
		if n := len(m.undo); n > 0 {
			step := m.undo[n-1]
			m.undo = m.undo[:n-1]
			m.tree = step.tree
			m.reflow()
			left := ""
			if len(m.undo) > 0 {
				left = fmt.Sprintf(" (%d more)", len(m.undo))
			}
			m.statusMsg, m.statusIsErr = "undid "+step.edit+left, false
		} else {
			m.statusMsg, m.statusIsErr = "nothing left to undo", true
		}
	case "c":
		m.confirmed = true
		m.done = true
		if m.embedded {
			return m, nil
		}
		return m, tea.Quit
	}
	m.scrollIntoView()
	return m, cmd
}

// mergeSelection folds the selected folders into one node under their lowest
// common ancestor, with the summed file count. See selectedRows for what
// counts as selected. The actual reshaping is vfs.MergeNodes' — this only
// resolves the row selection into IDs and applies the result back onto the
// row/undo/status state a tree edit knows nothing about.
func (m *Model) mergeSelection() {
	if !m.visualMode {
		m.statusMsg, m.statusIsErr = "press V to select folders, then m to merge", true
		return
	}
	sel := m.selectedRows()
	m.visualMode = false
	if len(sel) < 2 {
		m.statusMsg, m.statusIsErr = "select at least two folders at the same level to merge", true
		return
	}
	ids := make([]string, len(sel))
	for i, r := range sel {
		ids[i] = r.node.ID
	}

	m.snapshot("merge")
	newTree, mergedID, target, ancestor, err := vfs.MergeNodes(m.tree, ids, m.pendingNames())
	if err != nil {
		m.undo = m.undo[:len(m.undo)-1] // nothing was mutated, discard the snapshot
		m.statusMsg, m.statusIsErr = err.Error(), true
		return
	}
	m.tree = newTree

	m.reflow()
	if row := nodeRowByID(m.rows, mergedID); row != nil && target != row.node.Name {
		row.newName = target
	}
	m.focusNode(mergedID)

	m.statusMsg, m.statusIsErr = fmt.Sprintf("merged %d folders into %q under %q ([u] to undo)", len(ids), target, ancestor), false
}

// selectedRows are the rows [m]/[d]/[D] act on: in visual mode every row of the
// range at the anchor's depth, otherwise just the row under the cursor.
func (m *Model) selectedRows() []*reviewRow {
	if !m.visualMode {
		return []*reviewRow{m.rows[m.cursor]}
	}
	lo, hi := m.visualAnchor, m.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	// anchor depth is the rule: deeper rows are the selected folders' own
	// contents and ride along, shallower ones are scaffolding the range spanned
	// to reach the next branch
	depth := m.rows[m.visualAnchor].depth
	var out []*reviewRow
	for _, r := range m.rows[lo : hi+1] {
		if r.depth == depth {
			out = append(out, r)
		}
	}
	return out
}

// pendingNames maps node ID → the rename typed for it, so a merge compares the
// names folders will end up with, not the ones they start with.
func (m *Model) pendingNames() map[string]string {
	pending := map[string]string{}
	for _, r := range m.rows {
		if r.newName != "" {
			pending[r.node.ID] = r.newName
		}
	}
	return pending
}

// dropFolders removes each selected folder and lifts its children onto its
// parent — dropping "Apple iPhone 13" then "Indore" under 2023/April leaves
// April holding the files, one group-by level shallower. vfs.DropNodes does
// the actual reshaping; see mergeSelection's comment for the split.
func (m *Model) dropFolders(targets []*reviewRow) {
	ids := make([]string, len(targets))
	for i, r := range targets {
		ids[i] = r.node.ID
	}

	m.snapshot("drop")
	newTree, names, err := vfs.DropNodes(m.tree, ids)
	if err != nil {
		m.undo = m.undo[:len(m.undo)-1]
		m.statusMsg, m.statusIsErr = err.Error(), true
		return
	}
	m.tree = newTree

	m.reflow()
	m.visualMode = false
	what := fmt.Sprintf("dropped %q", names[0])
	if len(names) > 1 {
		what = fmt.Sprintf("dropped %d folders", len(names))
	}
	m.statusMsg, m.statusIsErr = what+" — their files moved up one level ([u] to undo)", false
}

// flattenFolders collapses everything below each selected folder into it, the
// folder itself staying put: `2023/April/Indore/Apple iPhone 13` flattened at
// April becomes `2023/April` holding all ten files. Works on a top-level row,
// unlike [d], since the Year survives to hold them.
//
// Over a [V] range the folders stay separate — folding them together is [m]'s
// job. FileCount is unchanged; it already counted the subtree. vfs.FlattenNodes
// does the actual reshaping; see mergeSelection's comment for the split.
func (m *Model) flattenFolders(targets []*reviewRow) {
	ids := make([]string, len(targets))
	for i, r := range targets {
		ids[i] = r.node.ID
	}

	m.snapshot("flatten")
	newTree, absorbed, names, err := vfs.FlattenNodes(m.tree, ids)
	if err != nil {
		m.undo = m.undo[:len(m.undo)-1]
		m.statusMsg, m.statusIsErr = err.Error(), true
		return
	}
	m.tree = newTree

	m.reflow()
	m.visualMode = false
	into := fmt.Sprintf("%q", names[len(names)-1])
	if len(names) > 1 {
		into = fmt.Sprintf("%d folders", len(names))
	}
	m.statusMsg, m.statusIsErr = fmt.Sprintf("flattened %d subfolders into %s ([u] to undo)", absorbed, into), false
}

// nodeRowByID finds the row for a node ID after a reflatten.
func nodeRowByID(rows []*reviewRow, id string) *reviewRow {
	for _, r := range rows {
		if r.node.ID == id {
			return r
		}
	}
	return nil
}

func (m Model) View() string {
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

// rowView renders one tree line: guide + name on the left, file count aligned
// at the right edge (the kit's right column), with the pending rename and any
// unaccepted suggestion shown inline. The cursor row and every row of a [V]
// range carry the highlight background — the same way the config wizard marks
// the option under the cursor.
func (m Model) rowView(i int, inRange bool) string {
	r := m.rows[i]

	cursor := "  "
	name := r.node.Name
	suggestion := ""
	if r.newName != "" && r.newName != r.node.Name {
		name += " → " + r.newName
	} else if len(r.node.Suggestions) > 0 {
		suggestion = "  ⇢ " + r.node.Suggestions[0].Name
	}
	count := fmt.Sprintf("%d files", r.node.FileCount)

	if inRange || i == m.cursor {
		// plain, no per-segment colour: an ANSI reset inside the line would cut
		// the background highlight short partway through
		if i == m.cursor {
			cursor = "❯ "
		}
		return tui.Selected.Render(tui.Row(cursor+r.guide+name+suggestion, count, m.width))
	}

	styled := r.node.Name
	if r.newName != "" && r.newName != r.node.Name {
		styled += tui.DimText.Render(" → ") + tui.OK.Render(r.newName)
	}
	if suggestion != "" {
		// amber, not dim: an unaccepted suggestion is the one thing on the row
		// that still wants a keypress
		styled += tui.Attn.Render(suggestion)
	}
	return tui.Row(cursor+tui.FaintTxt.Render(r.guide)+styled, tui.FaintTxt.Render(count), m.width)
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
			// same shape as the wizard's completion list: dim rows, and the top
			// match — what [tab] fills in — says so
			line := "    " + tui.FaintTxt.Render("· ") + tui.DimText.Render(s.label)
			if s.detail != "" {
				line += " " + tui.FaintTxt.Render("("+s.detail+")")
			}
			if i == 0 {
				line += tui.FaintTxt.Render("  ⇥ tab")
			}
			fmt.Fprintln(&b, tui.Row(line, "", m.width))
		}
		b.WriteString(tui.Footer(strings.Join([]string{
			tui.KeyHint("enter", "apply"),
			tui.KeyHint("tab", "use top match"),
			tui.KeyHint("ctrl+e", "wider search"),
			tui.KeyHint("esc", "cancel"),
		}, "   "), m.width))
	case m.previewing:
		b.WriteString(m.spin.View())
		b.WriteString(tui.DimText.Render(" Copying preview…"))
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
		tui.KeyHint("enter", "accept"),
		tui.KeyHint("a", "accept all"),
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
	hints = append(hints, tui.KeyHint("u", "undo"), tui.KeyHint("c", "save & exit"), tui.KeyHint("q", "discard"),
		tui.KeyHint("?", "help"))
	return strings.Join(hints, "   ")
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
			{"enter", "accept this folder's suggestion (never overwrites a rename you typed)"},
			{"a", "accept every suggestion in the tree"},
			{"r", "rename — type a name, or pick a nearby place; tab fills the top match"},
		}},
		{"Reshaping", []key{
			{"V", "start selecting folders; move the cursor to extend, esc to clear"},
			{"m", "merge the selected folders into one, under their common parent"},
			{"d", "drop the folder — its contents move up one level, the folder goes away"},
			{"D", "flatten — everything below moves directly into the folder"},
			{"u", "undo the last reshape; press again to walk further back"},
		}},
		{"Leaving", []key{
			{"p", "peek — copies a sample of the folder's files and opens them (read-only)"},
			{"c", "save the plan and exit — the only key that writes anything"},
			{"q", "exit without saving (warns once if you have unsaved edits)"},
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

/* --- preview: copy up to maxPreviewBytes of a folder's files to a temp dir and open it --- */

type previewDoneMsg struct {
	signature string
	dir       string
	err       error
}

// filesSignature keys a preview by file membership, not node ID: a parent and
// its only-child leaf carry the same files and should share one temp copy.
func filesSignature(files []string) string {
	return strings.Join(files, "\x00")
}

// peekCmd copies a folder's files to a temp dir for the OS viewer, reusing a
// cached copy with the same file membership. cached is a snapshot taken before
// dispatch — this runs off the UI goroutine.
func peekCmd(ctx context.Context, database *db.DB, node *vfs.Node, cached map[string]string) tea.Cmd {
	return func() tea.Msg {
		var files []string
		// a merged node's files still live under the folded-away paths until
		// Confirm rewrites them, so look under each
		for _, id := range append([]string{node.ID}, node.MergedIDs...) {
			under, err := vfs.FilesUnder(ctx, id, database)
			if err != nil {
				return previewDoneMsg{err: err}
			}
			files = append(files, under...)
		}
		if len(files) == 0 {
			return previewDoneMsg{err: fmt.Errorf("no files under %s", node.Name)}
		}
		sig := filesSignature(files)
		if dir, ok := cached[sig]; ok {
			return previewDoneMsg{signature: sig, dir: dir}
		}
		dir, err := os.MkdirTemp("", "wandersort-preview-*")
		if err != nil {
			return previewDoneMsg{err: err}
		}
		if _, err := copyFiles(ctx, files, dir, maxPreviewBytes, nil); err != nil {
			// nothing records this dir on the error path, so cleanupPreviewDirs
			// would never see it — drop the partial copy here instead
			os.RemoveAll(dir)
			return previewDoneMsg{err: err}
		}
		return previewDoneMsg{signature: sig, dir: dir}
	}
}

// cleanupPreviewDirs removes every temp dir peek copied files into — called
// once the review TUI exits, however it exits (confirm, quit, ctrl-c), so a
// review session never leaves preview copies behind.
func cleanupPreviewDirs(dirs map[string]string) {
	for _, dir := range dirs {
		os.RemoveAll(dir)
	}
}

// openInViewer opens a file or folder in the OS default viewer, best-effort.
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
