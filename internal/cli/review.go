package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/style"
	"github.com/jammutkarsh/wandersort/pkg/utils"
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
		if err := vfs.Confirm(ctx, sessionID, a.AppDB, tree); err != nil {
			return err
		}
	} else {
		// Rename autocomplete, expand-radius, and the [L] layout switch all
		// degrade gracefully without a resolver (e.g. location DB unreachable) —
		// not worth failing review over.
		if err := a.InitLocationResolver(ctx); err != nil {
			a.Log.Warn("Location resolver unavailable, rename suggestions disabled", "error", err)
		}
		m, err := tea.NewProgram(newReviewModel(tree, ctx, a.AppDB, sessionID, a.LocationResolver, a.Log),
			tea.WithOutput(os.Stderr), tea.WithAltScreen()).Run()
		if err != nil {
			return fmt.Errorf("review ui: %w", err)
		}
		rm := m.(reviewModel)
		defer cleanupPreviewDirs(rm.previewDirs)
		if !rm.confirmed {
			return fmt.Errorf("review cancelled — nothing changed")
		}
		for _, r := range rm.rows {
			if name := strings.TrimSpace(r.newName); name != "" {
				r.node.Name = name
			}
		}
		// rm.tree, not the outer tree: [L] may have rebuilt it with a new
		// layout mid-review, replacing every node's ID and the outer tree
		// variable would then be stale.
		if err := vfs.Confirm(ctx, sessionID, a.AppDB, rm.tree); err != nil {
			return err
		}
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
// parent identifies true siblings for the merge command — nil for top-level
// (Year) rows, which are siblings of each other too.
type reviewRow struct {
	node    *vfs.Node
	parent  *vfs.Node
	depth   int
	newName string
}

// flattenTree lays the whole tree out as indented rows so the reviewer can
// walk the proposed hierarchy top to bottom and rename any directory in place.
func flattenTree(nodes []vfs.Node, depth int, parent *vfs.Node) []*reviewRow {
	var rows []*reviewRow
	for i := range nodes {
		rows = append(rows, &reviewRow{node: &nodes[i], parent: parent, depth: depth})
		rows = append(rows, flattenTree(nodes[i].Children, depth+1, &nodes[i])...)
	}
	return rows
}

// resolvedName is what a row's directory would actually be named if confirmed
// right now: the edited name, else the top suggestion, else the proposed name.
func resolvedName(r *reviewRow) string {
	if r.newName != "" {
		return r.newName
	}
	if len(r.node.Suggestions) > 0 {
		return r.node.Suggestions[0].Name
	}
	return r.node.Name
}

// nameSuggestion is one rename candidate shown under the input: a ranked
// nearby place (geo) or a name confirmed in an earlier review (label).
type nameSuggestion struct {
	name   string
	detail string
}

const maxPreviewBytes = 250 * 1024 * 1024

// defaultCandidateRadius is the initial search width for rename suggestions;
// ctrl+e widens it by the same step (see location.queryNearest's own ladder).
const defaultCandidateRadius = 0.09

// layoutPreset is one selectable folder-depth shape, cycled with [L].
type layoutPreset struct {
	label   string
	groupBy []string // nil means vfs.Config{}'s zero value — flat Year/Month
}

// layoutPresets are the choices [L] cycles through. Index 0 matches
// vfs.DefaultConfig()'s GroupBy — not necessarily the layout this particular
// session actually started with (that depends on --group-by/config.yaml at
// scan time), so the first press might not be a no-op. ponytail: fixed list,
// not derived from the session's actual starting Config — good enough for a
// quick in-review toggle; revisit if users want custom presets.
var layoutPresets = []layoutPreset{
	{"Location + Orientation + Media", []string{vfs.GroupByLocation, vfs.GroupByOrientation, vfs.GroupByMedia}},
	{"Location only", []string{vfs.GroupByLocation}},
	{"Orientation + Media (no location)", []string{vfs.GroupByOrientation, vfs.GroupByMedia}},
	{"Flat Year/Month (no group-by)", nil},
}

type reviewModel struct {
	tree      []vfs.Node
	rows      []*reviewRow
	cursor    int
	offset    int // first visible row (scroll position)
	height    int
	editing   bool
	input     string
	confirmed bool

	ctx       context.Context
	db        *db.DB
	sessionID uuid.UUID
	resolver  *location.Resolver
	log       logger.Logger

	// rename autocomplete (populated while editing)
	suggestions []nameSuggestion
	radiusDelta float64

	// visual-select + merge (Vim-style: V selects, m merges, u undoes).
	// Merge reparents every leaf in the selected range under their lowest
	// common ancestor (see mergeSelection) — a structural tree edit, so undo
	// keeps a snapshot of the whole tree rather than per-row field values.
	visualMode    bool
	visualAnchor  int
	lastMergeTree []vfs.Node
	statusMsg     string
	statusIsErr   bool // statusMsg is a rejection/failure, not confirmation — rendered in a warning color so it isn't mistaken for routine info text

	// async preview copy (p key) — previewDirs caches one temp dir per node ID
	// so re-peeking the same folder reopens it instead of copying again;
	// runReview removes them all once the TUI exits, however it exits.
	previewing   bool
	previewErr   error
	previewDirs  map[string]string
	spinnerFrame int

	// async layout switch (L key) — rebuilds the whole proposal via vfs.Run,
	// so any renames typed under the old layout are gone once it lands
	// (different depth means different node IDs; there's nothing sensible to
	// carry over).
	layoutIdx   int
	relayouting bool
	relayoutErr error
}

func newReviewModel(tree []vfs.Node, ctx context.Context, database *db.DB, sessionID uuid.UUID, resolver *location.Resolver, log logger.Logger) reviewModel {
	return reviewModel{
		tree:        tree,
		rows:        flattenTree(tree, 0, nil),
		ctx:         ctx,
		db:          database,
		sessionID:   sessionID,
		resolver:    resolver,
		log:         log,
		previewDirs: map[string]string{},
	}
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

// refreshSuggestions repopulates the rename dropdown for the current row: geo
// candidates from the location resolver (filtered by whatever's typed so
// far), plus previously confirmed labels with the same prefix.
func (m *reviewModel) refreshSuggestions() {
	m.suggestions = nil
	row := m.rows[m.cursor]
	seen := map[string]bool{}
	prefix := strings.ToLower(strings.TrimSpace(m.input))

	if m.resolver != nil && row.node.Lat != nil && row.node.Lon != nil {
		delta := m.radiusDelta
		if delta == 0 {
			delta = defaultCandidateRadius
		}
		if cands, err := m.resolver.Candidates(m.ctx, *row.node.Lat, *row.node.Lon, delta, 8); err == nil {
			for _, c := range cands {
				if prefix != "" && !strings.HasPrefix(strings.ToLower(c.Name), prefix) {
					continue
				}
				if !seen[c.Name] {
					seen[c.Name] = true
					m.suggestions = append(m.suggestions, nameSuggestion{name: c.Name, detail: fmt.Sprintf("~%.0fkm", c.DistKM)})
				}
			}
		}
	}

	if m.db != nil && prefix != "" {
		var labels []string
		if err := m.db.SQL.SelectContext(m.ctx, &labels,
			`SELECT DISTINCT label FROM user_labels WHERE label LIKE ? || '%' COLLATE NOCASE ORDER BY label LIMIT 8`,
			m.input); err == nil {
			for _, l := range labels {
				if !seen[l] {
					seen[l] = true
					m.suggestions = append(m.suggestions, nameSuggestion{name: l, detail: "used before"})
				}
			}
		}
	}
}

func (m reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.scrollIntoView()
		return m, nil
	case tickMsg:
		if m.previewing || m.relayouting {
			m.spinnerFrame++
			return m, tickCmd()
		}
		return m, nil
	case previewDoneMsg:
		m.previewing = false
		m.previewErr = msg.err
		if msg.err == nil {
			m.previewDirs[msg.signature] = msg.dir
			openInViewer(msg.dir)
		}
		return m, nil
	case relayoutDoneMsg:
		m.relayouting = false
		m.relayoutErr = msg.err
		if msg.err == nil {
			m.tree = msg.tree
			m.rows = flattenTree(m.tree, 0, nil)
			m.cursor, m.offset = 0, 0
			m.visualMode, m.lastMergeTree = false, nil
			m.statusMsg, m.statusIsErr = "Layout: "+layoutPresets[m.layoutIdx].label+" — any in-progress renames were reset", false
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m reviewModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		switch key.Type {
		case tea.KeyEnter:
			m.rows[m.cursor].newName = m.input
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
				m.input = m.suggestions[0].name
				m.refreshSuggestions()
			}
		case tea.KeyCtrlE:
			m.radiusDelta += defaultCandidateRadius
			m.refreshSuggestions()
		}
		return m, nil
	}

	var cmd tea.Cmd
	switch key.String() {
	case "ctrl+c", "q":
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
	case "enter":
		// Precedence: default name < location suggestion < user's own rename —
		// never clobber a manual rename the reviewer already typed, even if
		// they land back on this row and press enter again.
		if r := m.rows[m.cursor]; r.newName == "" && len(r.node.Suggestions) > 0 {
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
		m.radiusDelta = 0
		m.refreshSuggestions()
	case "p":
		if !m.previewing {
			m.previewing = true
			m.previewErr = nil
			m.spinnerFrame = 0
			node := m.rows[m.cursor].node
			// snapshot, not the live map: peekCmd's closure runs on another
			// goroutine and must never touch state Update also mutates
			cached := make(map[string]string, len(m.previewDirs))
			maps.Copy(cached, m.previewDirs)
			cmd = tea.Batch(peekCmd(m.ctx, m.db, m.sessionID, node, cached), tickCmd())
		}
	case "a":
		for _, r := range m.rows {
			if r.newName == "" && len(r.node.Suggestions) > 0 {
				r.newName = r.node.Suggestions[0].Name
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
	case "L":
		if !m.relayouting && m.resolver != nil {
			m.layoutIdx = (m.layoutIdx + 1) % len(layoutPresets)
			m.relayouting = true
			m.relayoutErr = nil
			m.spinnerFrame = 0
			cmd = tea.Batch(relayoutCmd(m.ctx, m.db, m.resolver, m.log, m.sessionID, layoutPresets[m.layoutIdx].groupBy), tickCmd())
		}
	case "m":
		m.mergeSelection()
	case "u":
		if m.lastMergeTree != nil {
			m.tree = m.lastMergeTree
			m.rows = flattenTree(m.tree, 0, nil)
			m.lastMergeTree = nil
			m.statusMsg, m.statusIsErr = "undid last merge", false
		}
	case "c":
		m.confirmed = true
		return m, tea.Quit
	}
	m.scrollIntoView()
	return m, cmd
}

// mergeSelection folds every *leaf* row in the visually-selected range
// (contiguous, sequential — no picking rows out of order) into a single node
// under their lowest common ancestor, named after the first leaf's resolved
// name. The merged-away leaves' IDs ride along on Node.MergedIDs so Confirm
// still remaps their files onto the surviving node's path — the reviewer sees
// exactly one folder with the combined file count, which is the whole point
// of asking for a merge.
//
// Rows in the range that still have children (the Month/Day/etc. rows
// spanned to reach a leaf in another branch) are structural scaffolding, not
// merge candidates themselves — they don't merge, but any of them left with
// no children at all once the leaves move out is pruned, since an empty
// Month/Day folder is not something the reviewer should still be looking at.
func (m *reviewModel) mergeSelection() {
	if !m.visualMode {
		m.statusMsg, m.statusIsErr = "press V to select folders, then m to merge", true
		return
	}
	lo, hi := m.visualAnchor, m.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	sel := m.rows[lo : hi+1]
	m.visualMode = false

	type leaf struct {
		row    *reviewRow
		id     string
		parent *vfs.Node
		value  vfs.Node
	}
	var leaves []leaf
	for _, r := range sel {
		if len(r.node.Children) == 0 {
			leaves = append(leaves, leaf{row: r, id: r.node.ID, parent: r.parent, value: *r.node})
		}
	}
	if len(leaves) < 2 {
		m.statusMsg, m.statusIsErr = "select at least two folders with no subfolders to merge", true
		return
	}

	lcaID := leaves[0].id
	for _, l := range leaves[1:] {
		lcaID = commonPathPrefix(lcaID, l.id)
	}
	if lcaID == "" {
		m.statusMsg, m.statusIsErr = "selected folders share no common ancestor to merge under", true
		return
	}
	lca := findNodeByID(m.tree, lcaID)
	if lca == nil {
		m.statusMsg, m.statusIsErr = "internal error locating merge destination", true
		return
	}

	m.lastMergeTree = deepCloneNodes(m.tree)

	// which nodes are leaves *before* the splice — afterwards, a childless node
	// is either one of these or an ancestor emptied by the merge (prune it)
	leafIDs := map[string]bool{}
	collectLeafIDs(m.tree, leafIDs)

	target := resolvedName(leaves[0].row)
	merged := leaves[0].value
	for _, l := range leaves[1:] {
		merged.MergedIDs = append(merged.MergedIDs, l.id)
		merged.FileCount += l.value.FileCount
		merged.Samples = append(merged.Samples, l.value.Samples...)
	}
	for _, l := range leaves {
		if l.parent != nil {
			removeChildByID(l.parent, l.id)
		}
	}
	lca.Children = append(lca.Children, merged)
	m.tree, _ = pruneEmptied(m.tree, leafIDs)

	m.rows = flattenTree(m.tree, 0, nil)
	if row := nodeRowByID(m.rows, merged.ID); row != nil {
		row.newName = target
	}

	m.statusMsg, m.statusIsErr = fmt.Sprintf("merged %d folders into %q under %q ([u] to undo)", len(leaves), target, lca.Name), false
}

// collectLeafIDs records every childless node's ID, used to tell a real leaf
// from an ancestor a merge emptied out.
func collectLeafIDs(nodes []vfs.Node, out map[string]bool) {
	for i := range nodes {
		if len(nodes[i].Children) == 0 {
			out[nodes[i].ID] = true
		}
		collectLeafIDs(nodes[i].Children, out)
	}
}

// pruneEmptied drops the ancestors a merge left with no children (they held
// nothing but the leaves that moved away) and refreshes FileCount bottom-up,
// so the counts on the surviving chain match what's actually under them.
// leafIDs is the pre-merge leaf set — anything childless outside it is an
// emptied ancestor. Returns the kept nodes and their total file count.
func pruneEmptied(nodes []vfs.Node, leafIDs map[string]bool) ([]vfs.Node, int) {
	out, total := nodes[:0], 0
	for i := range nodes {
		n := nodes[i]
		if len(n.Children) > 0 {
			kept, sum := pruneEmptied(n.Children, leafIDs)
			if len(kept) == 0 {
				continue
			}
			n.Children, n.FileCount = kept, sum
		} else if !leafIDs[n.ID] {
			continue
		}
		total += n.FileCount
		out = append(out, n)
	}
	return out, total
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

// commonPathPrefix returns the longest shared leading run of "/"-separated
// segments between two node IDs (which are literally their proposed
// directory paths) — i.e. their lowest common ancestor's ID. "" means no
// shared ancestor at all (e.g. different years).
func commonPathPrefix(a, b string) string {
	as := strings.Split(a, "/")
	bs := strings.Split(b, "/")
	n := min(len(as), len(bs))
	var i int
	for i = 0; i < n; i++ {
		if as[i] != bs[i] {
			break
		}
	}
	return strings.Join(as[:i], "/")
}

// findNodeByID searches the tree for the node with the given ID.
func findNodeByID(nodes []vfs.Node, id string) *vfs.Node {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
		if found := findNodeByID(nodes[i].Children, id); found != nil {
			return found
		}
	}
	return nil
}

// removeChildByID removes the child with the given ID from parent's
// Children, if present.
func removeChildByID(parent *vfs.Node, id string) {
	for i, c := range parent.Children {
		if c.ID == id {
			parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
			return
		}
	}
}

// deepCloneNodes recursively copies a node tree so a snapshot taken before a
// structural edit (merge) is unaffected by later in-place mutation — used
// for [u] undo.
func deepCloneNodes(nodes []vfs.Node) []vfs.Node {
	if nodes == nil {
		return nil
	}
	out := make([]vfs.Node, len(nodes))
	for i, n := range nodes {
		out[i] = n
		out[i].Children = deepCloneNodes(n.Children)
		if n.Samples != nil {
			out[i].Samples = append([]string(nil), n.Samples...)
		}
		if n.Suggestions != nil {
			out[i].Suggestions = append([]vfs.Suggestion(nil), n.Suggestions...)
		}
		if n.MergedIDs != nil {
			out[i].MergedIDs = append([]string(nil), n.MergedIDs...)
		}
		if n.Lat != nil {
			lat := *n.Lat
			out[i].Lat = &lat
		}
		if n.Lon != nil {
			lon := *n.Lon
			out[i].Lon = &lon
		}
	}
	return out
}

func (m reviewModel) View() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.Header.Render("Review proposed folders"))
	fmt.Fprintln(&b, style.Dim.Render("Walk the proposed hierarchy — rename any folder, accept suggestions on unresolved ones."))
	fmt.Fprintln(&b)

	selLo, selHi := -1, -1
	if m.visualMode {
		selLo, selHi = m.visualAnchor, m.cursor
		if selLo > selHi {
			selLo, selHi = selHi, selLo
		}
	}

	end := min(m.offset+m.visibleRows(), len(m.rows))
	for i := m.offset; i < end; i++ {
		r := m.rows[i]
		selected := selLo != -1 && i >= selLo && i <= selHi

		indent := strings.Repeat("  ", r.depth)
		branch := ""
		if r.depth > 0 {
			branch = "└ "
		}
		name := r.node.Name
		if r.newName != "" && r.newName != r.node.Name {
			name = r.node.Name + " → " + r.newName
		}
		info := fmt.Sprintf("%d files", r.node.FileCount)
		if len(r.node.Suggestions) > 0 {
			info += ", suggested: " + r.node.Suggestions[0].Name
		}

		cursor := "  "
		if i == m.cursor {
			cursor = style.Warn.Render("> ")
		}
		if selected {
			// The whole line gets a background highlight — clearer across a
			// full row than a marker in one column. Rendered plain (no
			// nested per-segment styling below) since an ANSI reset inside
			// an already-styled substring would cut the background short
			// partway through the line.
			line := fmt.Sprintf("%s%s%s  (%s)", indent, branch, name, info)
			fmt.Fprintf(&b, "%s%s\n", cursor, style.Selected.Render(line))
		} else {
			styledBranch := ""
			if branch != "" {
				styledBranch = style.Dim.Render(branch)
			}
			styledName := r.node.Name
			if r.newName != "" && r.newName != r.node.Name {
				styledName = r.node.Name + " → " + style.Success.Render(r.newName)
			}
			fmt.Fprintf(&b, "%s%s%s%s  %s\n", cursor, indent, styledBranch, styledName, style.Dim.Render("("+info+")"))
		}
	}

	fmt.Fprintln(&b)
	switch {
	case m.editing:
		fmt.Fprintf(&b, "%s%s█\n", style.Warn.Render("New name: "), m.input)
		for _, s := range m.suggestions {
			fmt.Fprintf(&b, "  %s %s\n", s.name, style.Dim.Render("("+s.detail+")"))
		}
		fmt.Fprintln(&b, style.Dim.Render("[enter] apply  [tab] use top match  [ctrl+e] wider search  [esc] cancel"))
	case m.previewing:
		frames := []string{"|", "/", "-", "\\"}
		fmt.Fprintf(&b, "%s Copying preview…\n", frames[m.spinnerFrame%len(frames)])
	case m.relayouting:
		frames := []string{"|", "/", "-", "\\"}
		fmt.Fprintf(&b, "%s Rebuilding proposal with layout %q…\n", frames[m.spinnerFrame%len(frames)], layoutPresets[m.layoutIdx].label)
	default:
		if m.previewErr != nil {
			fmt.Fprintln(&b, style.Err.Render("Preview failed: "+m.previewErr.Error()))
		}
		if m.relayoutErr != nil {
			fmt.Fprintln(&b, style.Err.Render("Layout switch failed: "+m.relayoutErr.Error()))
		}
		if m.statusMsg != "" {
			if m.statusIsErr {
				fmt.Fprintln(&b, style.Warn.Render(m.statusMsg))
			} else {
				fmt.Fprintln(&b, style.Dim.Render(m.statusMsg))
			}
		}
		fmt.Fprintln(&b, style.Dim.Render("[↑/↓] move  [enter] accept suggestion  [r] rename  [p] peek  [a] accept all  [V] select  [m] merge  [u] undo  [L] layout  [c] confirm  [q] quit"))
	}
	return b.String()
}

/* --- preview: copy up to maxPreviewBytes of a folder's files to a temp dir and open it --- */

type previewDoneMsg struct {
	signature string
	dir       string
	err       error
}

type tickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

// filesSignature identifies a folder's preview by its actual file membership,
// not the tree node it was peeked from — two different nodes (e.g. a leaf
// "Photos" folder and its only-child parent) can carry the exact same files,
// and should share one cached copy instead of a fresh one each time.
func filesSignature(files []string) string {
	return strings.Join(files, "\x00")
}

// peekCmd copies a folder's files into a temp dir for the OS viewer, unless
// cached (a snapshot of previewDirs taken before this Cmd was dispatched —
// see the "p" key handler) already has a copy with the same file membership,
// in which case it's reused instead of copied again.
func peekCmd(ctx context.Context, database *db.DB, sessionID uuid.UUID, node *vfs.Node, cached map[string]string) tea.Cmd {
	return func() tea.Msg {
		var files []string
		// a merged node's files still live under the folded-away nodes' paths
		// until Confirm rewrites them, so peek has to look under each of them
		for _, id := range append([]string{node.ID}, node.MergedIDs...) {
			under, err := vfs.FilesUnder(ctx, sessionID, id, database)
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
		if _, err := utils.CopyFiles(ctx, files, dir, maxPreviewBytes, nil); err != nil {
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

/* --- layout switch: rebuild the whole proposal with a different Config.GroupBy --- */

type relayoutDoneMsg struct {
	tree []vfs.Node
	err  error
}

// relayoutCmd re-runs VFS phase 4 in place with a different group-by — vfs.Run
// only reads already-hashed masters from the DB and replaces the proposal set
// wholesale, so this is safe to call mid-review without touching the
// filesystem or re-scanning/re-hashing anything. Reuses sessionID so the
// rebuilt rows still belong to the session under review.
func relayoutCmd(ctx context.Context, database *db.DB, resolver *location.Resolver, log logger.Logger, sessionID uuid.UUID, groupBy []string) tea.Cmd {
	return func() tea.Msg {
		cfg := vfs.DefaultConfig()
		cfg.GroupBy = groupBy
		if _, err := vfs.New(database, resolver, log, cfg).Run(ctx, sessionID); err != nil {
			return relayoutDoneMsg{err: err}
		}
		tree, err := vfs.BuildTree(ctx, sessionID, database)
		if err != nil {
			return relayoutDoneMsg{err: err}
		}
		return relayoutDoneMsg{tree: tree}
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
