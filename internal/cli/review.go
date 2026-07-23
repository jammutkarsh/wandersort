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
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
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
wandersort review --yes

# Re-propose the hierarchy with the current --group-by/config.yaml first
wandersort review --rebuild --group-by device,media`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReview()
		},
	}

	cmd.Flags().Bool(flagYes, false, "Accept all suggestions without prompting")
	cmd.Flags().Bool(flagRebuild, false,
		"Re-run the VFS proposal with the current group-by before reviewing (no re-scan or re-hash)")
	cmd.Flags().StringSlice(flagGroupBy, nil,
		"Folder levels below Year/Month: location, date, device, orientation, media, or none (only with --rebuild)")
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

	// --rebuild re-proposes the hierarchy from the metadata already in the DB,
	// so a changed group-by (flag, env, or config.yaml — applyOverrides has
	// resolved all three by now) takes effect without re-scanning or
	// re-hashing anything. vfs.Run replaces the session's proposal wholesale.
	if v.GetBool(flagRebuild) {
		// vfs.Run deletes every existing entry, so a plan the user already
		// confirmed (APPROVED rows carrying their edited target_path) is gone
		// with it. The *names* survive in user_labels and come back as
		// suggestions, but silently dropping confirmed work is not something
		// to do on a bare flag — make it explicit.
		approved, err := approvedCount(ctx, a.AppDB, sessionID)
		if err != nil {
			return err
		}
		if approved > 0 && !v.GetBool(flagYes) {
			return fmt.Errorf("--rebuild would discard the confirmed plan for this session (%d approved files).\n"+
				"Your confirmed folder names are remembered and will be re-suggested; re-run with --yes to rebuild and re-accept them", approved)
		}
		// EnsureDependencies already opens the resolver (and holds the install
		// lock while it does), so no separate InitLocationResolver here.
		if err := a.EnsureDependencies(ctx); err != nil {
			return fmt.Errorf("dependencies: %w", err)
		}
		cfg := vfs.ConfigFor(a.Config)
		a.Log.Info("Rebuilding folder proposal", "groupBy", cfg.GroupBy, logger.UserKey, true)
		if _, err := vfs.New(a.AppDB, a.LocationResolver, a.Log, cfg).Run(ctx, sessionID); err != nil {
			return fmt.Errorf("rebuild proposal: %w", err)
		}
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
			if rm.relayouted {
				// [L] already ran vfs.Run, which replaced the session's rows —
				// claiming nothing changed would be a lie
				return fmt.Errorf("review cancelled — folder names unchanged, but [L] rebuilt the proposal with layout %q; re-run 'wandersort scan' to go back",
					layoutPresets[rm.layoutIdx].label)
			}
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

	// review is the last look before a plan is approved — finding out mid-move
	// that the output volume is too small is far worse than being told here
	workflow.CheckOutputSpace(ctx, a.AppDB, a.Log, filepath.Dir(a.Config.AppDBPath), sessionID)

	fmt.Fprintln(os.Stderr, style.Success.Render("Folder structure approved.")+
		style.Dim.Render(" Confirmed names will be suggested on future scans."))
	return nil
}

// approvedCount is how many of the session's entries the user already
// confirmed — the size of the plan a --rebuild would throw away.
func approvedCount(ctx context.Context, database *db.DB, sessionID uuid.UUID) (int, error) {
	var n int
	if err := database.SQL.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM virtual_fs_entries WHERE session_id = ? AND status = ?`,
		sessionID.String(), db.StatusApproved); err != nil {
		return 0, fmt.Errorf("count approved entries: %w", err)
	}
	return n, nil
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

// maxSuggestions caps the rename dropdown — it renders above the key help, so
// an unbounded list would push the tree off screen.
const maxSuggestions = 8

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
	width     int
	editing   bool
	input     string
	confirmed bool

	ctx       context.Context
	db        *db.DB
	sessionID uuid.UUID
	resolver  *location.Resolver
	log       logger.Logger

	// Rename autocomplete. Both sources are fetched up front and filtered in
	// memory per keystroke: labels once at startup (they change only on
	// Confirm, after the TUI is gone), geoCands once per rename via
	// loadGeoCandidates. radiusDelta is the live search width, seeded to
	// defaultCandidateRadius when [r] opens the editor so the first ctrl+e
	// genuinely widens instead of re-running the default.
	suggestions []nameSuggestion
	geoCands    []location.Candidate
	labels      []string
	radiusDelta float64

	// Structural edits (Vim-style: V selects, m merges, d/D delete a folder or
	// a whole level, u undoes). Both reshape the tree rather than just editing
	// names, so undo keeps a whole-tree snapshot instead of per-row field
	// values — undo keeps a stack of them, one per edit, so [u] walks all the
	// way back rather than only undoing the most recent one.
	visualMode   bool
	visualAnchor int
	quitWarned   bool // [q] with pending edits warns once before discarding them
	undo         []undoStep
	statusMsg    string
	statusIsErr  bool // statusMsg is a rejection/failure, not confirmation — rendered in a warning color so it isn't mistaken for routine info text

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
	// relayouted records that [L] already persisted a new proposal set, so
	// quitting can't claim nothing changed — it did, on disk, irreversibly.
	relayouted bool
}

func newReviewModel(tree []vfs.Node, ctx context.Context, database *db.DB, sessionID uuid.UUID, resolver *location.Resolver, log logger.Logger) reviewModel {
	m := reviewModel{
		tree:        tree,
		rows:        flattenTree(tree, 0, nil),
		ctx:         ctx,
		db:          database,
		sessionID:   sessionID,
		resolver:    resolver,
		log:         log,
		previewDirs: map[string]string{},
	}
	// Confirm is the only thing that writes user_labels, and it runs after this
	// TUI exits — so the set is fixed for the whole session and worth loading
	// once here instead of re-querying it as the reviewer types. Failure just
	// means no "used before" suggestions; not worth refusing to review over.
	if database != nil {
		if err := database.SQL.SelectContext(ctx, &m.labels,
			`SELECT DISTINCT label FROM user_labels ORDER BY label`); err != nil && log != nil {
			log.Warn("Could not load confirmed labels for rename suggestions", "error", err)
		}
	}
	return m
}

func (m reviewModel) Init() tea.Cmd { return nil }

// headerLines is the fixed block above the tree: title, subtitle, blank.
const headerLines = 3

// visibleRows is how many tree lines fit between the header and the footer.
// The footer is measured, not assumed to be one line: the key help wraps to
// several lines on a narrow terminal, and a fixed guess there pushed the tree
// off the bottom of the screen.
func (m reviewModel) visibleRows() int {
	return max(m.height-headerLines-strings.Count(m.footer(), "\n"), 1)
}

// wrapDim renders dimmed text word-wrapped to the terminal width, so a long
// line (the key help, a status message) never runs off the edge. Width 0 means
// no WindowSizeMsg has arrived yet — render unwrapped rather than to nothing.
func (m reviewModel) wrapDim(s string) string {
	if m.width <= 0 {
		return style.Dim.Render(s)
	}
	return style.Dim.Width(m.width).Render(s)
}

// reflow rebuilds the row list after a structural edit (merge, delete, undo),
// carrying every pending rename across by node ID. flattenTree allocates fresh
// reviewRow values, so without this a splice silently discards renames typed
// on rows it never touched. Cursor is clamped since the tree may have shrunk.
func (m *reviewModel) reflow() {
	sortTree(m.tree)
	pending := map[string]string{}
	for _, r := range m.rows {
		if r.newName != "" {
			pending[r.node.ID] = r.newName
		}
	}
	m.rows = flattenTree(m.tree, 0, nil)
	for _, r := range m.rows {
		if name, ok := pending[r.node.ID]; ok {
			r.newName = name
		}
	}
	m.cursor = min(m.cursor, len(m.rows)-1)
}

// undoStep is the tree as it stood before one structural edit, plus what that
// edit was, for the status line.
type undoStep struct {
	tree []vfs.Node
	edit string
}

// maxUndo caps how far back [u] can walk. Each step is a full clone of the
// directory tree (folders only, never files), so the cost is small, but an
// unbounded stack in a long review session is unbounded memory for no reason.
const maxUndo = 100

// snapshot records the tree before a structural edit so [u] can walk back to
// it. Called by every edit that reshapes the tree — merge, drop, flatten.
func (m *reviewModel) snapshot(edit string) {
	m.undo = append(m.undo, undoStep{tree: deepCloneNodes(m.tree), edit: edit})
	if len(m.undo) > maxUndo {
		m.undo = m.undo[len(m.undo)-maxUndo:]
	}
}

// hasEdits reports whether anything would be lost by quitting: a typed or
// accepted rename, or a structural edit still on the undo stack. Derived
// rather than tracked with a flag so no edit path can forget to set one.
func (m reviewModel) hasEdits() bool {
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
// the cursor's own indent depth, wherever it is in the tree — the sibling-level
// hop that makes a deep tree walkable without scrolling through every folder's
// contents on the way. It crosses into other branches by design: that is what
// lets [V] then [n][n] select the same level across several months. Stops at
// the ends rather than wrapping, so holding the key can't silently loop.
func (m *reviewModel) jumpSameDepth(step int) {
	depth := m.rows[m.cursor].depth
	for i := m.cursor + step; i >= 0 && i < len(m.rows); i += step {
		if m.rows[i].depth == depth {
			m.cursor = i
			m.statusMsg, m.statusIsErr = "", false
			return
		}
	}
	m.statusMsg, m.statusIsErr = "no more folders at this level", true
}

// sortTree restores name order after a structural edit. BuildTree emits every
// level sorted, but edits append — a merged node, or children lifted by a drop
// — at the end of the parent's list. A 575-file folder suddenly sitting below
// its siblings instead of between them reads as "the merge deleted it", which
// is exactly what it was reported as.
func sortTree(nodes []vfs.Node) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	for i := range nodes {
		sortTree(nodes[i].Children)
	}
}

// focusNode puts the cursor on a node by ID, so an edit that moves a folder
// leaves the reviewer looking at where it went rather than at whatever row
// happens to sit at the old index.
func (m *reviewModel) focusNode(id string) {
	for i, r := range m.rows {
		if r.node.ID == id {
			m.cursor = i
			return
		}
	}
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

// loadGeoCandidates queries the location resolver for the current row and
// caches the result on the model. Called only when a rename actually starts
// ([r]) or the reviewer widens the radius (ctrl+e) — never per keystroke, which
// is what refreshSuggestions filters over in memory.
func (m *reviewModel) loadGeoCandidates() {
	m.geoCands = nil
	row := m.rows[m.cursor]
	if m.resolver == nil || row.node.Lat == nil || row.node.Lon == nil {
		return
	}
	// generous limit: the typed prefix narrows this list in memory, so fetching
	// once has to cover every prefix the reviewer might type, not just the
	// handful shown at any moment
	if cands, err := m.resolver.Candidates(m.ctx, *row.node.Lat, *row.node.Lon, m.radiusDelta, 64); err == nil {
		m.geoCands = cands
	}
}

// refreshSuggestions repopulates the rename dropdown from data already in
// memory — nearby places fetched once by loadGeoCandidates, plus the confirmed
// labels loaded once at startup. Pure filtering, no I/O: it runs on every
// keystroke, and a query per keystroke made typing lag on a cold cache.
func (m *reviewModel) refreshSuggestions() {
	m.suggestions = nil
	seen := map[string]bool{}
	prefix := strings.ToLower(strings.TrimSpace(m.input))

	for _, c := range m.geoCands {
		if prefix != "" && !strings.HasPrefix(strings.ToLower(c.Name), prefix) {
			continue
		}
		if !seen[c.Name] {
			seen[c.Name] = true
			m.suggestions = append(m.suggestions, nameSuggestion{name: c.Name, detail: fmt.Sprintf("~%.0fkm", c.DistKM)})
		}
		if len(m.suggestions) >= maxSuggestions {
			return
		}
	}

	if prefix != "" {
		for _, l := range m.labels {
			if !strings.HasPrefix(strings.ToLower(l), prefix) {
				continue
			}
			if !seen[l] {
				seen[l] = true
				m.suggestions = append(m.suggestions, nameSuggestion{name: l, detail: "used before"})
			}
			if len(m.suggestions) >= maxSuggestions {
				return
			}
		}
	}
}

func (m reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height, m.width = msg.Height, msg.Width
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
			m.relayouted = true
			m.tree = msg.tree
			m.rows = flattenTree(m.tree, 0, nil)
			m.cursor, m.offset = 0, 0
			m.visualMode, m.undo = false, nil
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
			m.loadGeoCandidates() // the only key that re-queries mid-rename
			m.refreshSuggestions()
		}
		return m, nil
	}

	var cmd tea.Cmd
	// the "press q again" warning only stands for the very next keypress
	m.quitWarned = m.quitWarned && key.String() == "q"
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "q":
		// quitting throws away every rename and structural edit — make the
		// reviewer say so twice, and point at the key that saves instead
		if m.hasEdits() && !m.quitWarned {
			m.quitWarned = true
			m.statusMsg, m.statusIsErr = "unsaved changes — [c] saves and exits, press q again to discard them", true
			break
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
		m.radiusDelta = defaultCandidateRadius
		m.loadGeoCandidates()
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
	case "d":
		m.dropFolders(m.selectedRows())
	case "D":
		m.flattenFolders(m.selectedRows())
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
		return m, tea.Quit
	}
	m.scrollIntoView()
	return m, cmd
}

// mergeSelection folds the selected folders into a single node under their
// lowest common ancestor, named after the first one's resolved name. The
// folded-away IDs ride along on Node.MergedIDs so Confirm still remaps their
// files onto the surviving node's path — the reviewer sees exactly one folder
// with the combined file count, which is the whole point of asking for a merge.
//
// Which rows count as "selected" is decided by the depth of the row [V] was
// pressed on: every row in the range at that same depth. Deeper rows in the
// range are those folders' own contents and come along with them; shallower
// ones are scaffolding spanned to reach the next branch. That makes both
// shapes work with one rule — leaves from different Month branches (anchor on
// a leaf), and whole Day folders from one trip (anchor on a Day).
//
// Merging parents merges their subtrees: children with the same final name
// collapse recursively (three days in Goa produce one Goa, not three), since
// leaving the reviewer to merge each identical child by hand is exactly the
// work they asked the parent merge to do. Ancestors emptied by the splice are
// pruned.
func (m *reviewModel) mergeSelection() {
	if !m.visualMode {
		m.statusMsg, m.statusIsErr = "press V to select folders, then m to merge", true
		return
	}
	sel := m.selectedRows()
	m.visualMode = false

	type pick struct {
		row    *reviewRow
		id     string
		parent *vfs.Node
		value  vfs.Node
	}
	picks := make([]pick, 0, len(sel))
	for _, r := range sel {
		picks = append(picks, pick{row: r, id: r.node.ID, parent: r.parent, value: *r.node})
	}
	if len(picks) < 2 {
		m.statusMsg, m.statusIsErr = "select at least two folders at the same level to merge", true
		return
	}

	lcaID := picks[0].id
	for _, p := range picks[1:] {
		lcaID = commonPathPrefix(lcaID, p.id)
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

	m.snapshot("merge")

	// which nodes are leaves *before* the splice — afterwards, a childless node
	// is either one of these or an ancestor emptied by the merge (prune it)
	leafIDs := map[string]bool{}
	collectLeafIDs(m.tree, leafIDs)

	// the merged folder takes the first pick's *own* name, or the rename the
	// reviewer typed on it — never its suggestion. A suggestion is an offer
	// the reviewer hasn't accepted; broadcasting one across a merge puts a
	// name on the folder that nobody chose, and it isn't obvious which of the
	// selected rows it even came from.
	pending := m.pendingNames()
	target := finalName(picks[0].value, pending)
	merged := picks[0].value
	for _, p := range picks[1:] {
		mergeInto(&merged, p.value, pending)
	}
	for _, p := range picks {
		if p.parent != nil {
			removeChildByID(p.parent, p.id)
		}
	}
	lca.Children = append(lca.Children, merged)
	m.tree, _ = pruneEmptied(m.tree, leafIDs)

	m.reflow()
	if row := nodeRowByID(m.rows, merged.ID); row != nil && target != row.node.Name {
		row.newName = target
	}
	m.focusNode(merged.ID)

	m.statusMsg, m.statusIsErr = fmt.Sprintf("merged %d folders into %q under %q ([u] to undo)", len(picks), target, lca.Name), false
}

// selectedRows are the rows a structural command (merge, drop, flatten) acts
// on: in visual mode every row of the range at the depth of the row [V] was
// pressed on, otherwise just the row under the cursor.
//
// The anchor-depth rule is what lets a range mean something for all three
// commands. Rows deeper than the anchor are the selected folders' own
// contents and come along with them; shallower ones are scaffolding the range
// spanned to reach the next branch. So selecting several locations under a
// Day and pressing [D] flattens each of them, without also trying to act on
// the Day above or the splits below.
func (m *reviewModel) selectedRows() []*reviewRow {
	if !m.visualMode {
		return []*reviewRow{m.rows[m.cursor]}
	}
	lo, hi := m.visualAnchor, m.cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	depth := m.rows[m.visualAnchor].depth
	var out []*reviewRow
	for _, r := range m.rows[lo : hi+1] {
		if r.depth == depth {
			out = append(out, r)
		}
	}
	return out
}

// pendingNames maps node ID → the rename typed for it, so a merge can tell
// whether two folders will *end up* with the same name, not just whether they
// start with it (Confirm uses the typed name when there is one).
func (m *reviewModel) pendingNames() map[string]string {
	pending := map[string]string{}
	for _, r := range m.rows {
		if r.newName != "" {
			pending[r.node.ID] = r.newName
		}
	}
	return pending
}

// mergeInto folds src into dst: counts add up, src's ID (and anything already
// folded into src) is recorded for Confirm, and children are absorbed —
// same-named ones recursively merged rather than left as duplicate siblings.
func mergeInto(dst *vfs.Node, src vfs.Node, pending map[string]string) {
	dst.FileCount += src.FileCount
	dst.Samples = append(dst.Samples, src.Samples...)
	dst.MergedIDs = append(dst.MergedIDs, src.ID)
	dst.MergedIDs = append(dst.MergedIDs, src.MergedIDs...)
	for _, c := range src.Children {
		if twin := childByName(dst, finalName(c, pending), pending); twin != nil {
			mergeInto(twin, c, pending)
			continue
		}
		dst.Children = append(dst.Children, c)
	}
}

// finalName is the segment a node will actually be written as: the reviewer's
// rename if they typed one, else the proposed name. Suggestions don't count —
// they only apply once accepted.
func finalName(n vfs.Node, pending map[string]string) string {
	if name, ok := pending[n.ID]; ok {
		return name
	}
	return n.Name
}

// childByName finds parent's child that will end up named name, if any.
func childByName(parent *vfs.Node, name string, pending map[string]string) *vfs.Node {
	for i := range parent.Children {
		if finalName(parent.Children[i], pending) == name {
			return &parent.Children[i]
		}
	}
	return nil
}

// dropFolders removes folders the reviewer doesn't want: each one's children
// are lifted onto its parent, and its own ID (plus anything already folded
// into it) goes onto the parent's MergedIDs so Confirm remaps the files that
// sat directly in it up to the parent's path too. Dropping "Apple iPhone 13"
// then "Indore" under 2023/April leaves April holding the files — the
// group-by level is gone from that branch, nothing is lost.
//
// Top-level rows are refused: their files have nowhere to go but the library
// root. Use [D] to flatten a Year instead, which keeps the Year itself.
//
// Targets are addressed by parent ID and the parent re-found per drop, since
// removing one child reslices the parent's Children and invalidates row
// pointers into it. selectedRows only ever returns rows at one depth, so no
// target here is an ancestor of another.
func (m *reviewModel) dropFolders(targets []*reviewRow) {
	type drop struct {
		parentID string
		node     vfs.Node
	}
	drops := make([]drop, 0, len(targets))
	for _, r := range targets {
		if r.parent == nil {
			m.statusMsg, m.statusIsErr = "can't drop a top-level folder — its files would land in the library root ([D] flattens it instead)", true
			return
		}
		drops = append(drops, drop{parentID: r.parent.ID, node: *r.node})
	}
	if len(drops) == 0 {
		return
	}

	m.snapshot("drop")
	for _, d := range drops {
		parent := findNodeByID(m.tree, d.parentID)
		if parent == nil {
			continue
		}
		removeChildByID(parent, d.node.ID)
		parent.Children = append(parent.Children, d.node.Children...)
		parent.MergedIDs = append(parent.MergedIDs, append([]string{d.node.ID}, d.node.MergedIDs...)...)
	}

	m.reflow()
	m.visualMode = false
	what := fmt.Sprintf("dropped %q", drops[0].node.Name)
	if len(drops) > 1 {
		what = fmt.Sprintf("dropped %d folders", len(drops))
	}
	m.statusMsg, m.statusIsErr = what+" — their files moved up one level ([u] to undo)", false
}

// flattenFolders collapses everything *below* each selected folder into it:
// that subtree's files end up sitting directly in the one folder, which itself
// stays put. `2023/April/Indore/Apple iPhone 13` flattened at April becomes
// `2023/April` holding all ten files.
//
// With a [V] range this runs per selected folder, independently — several
// locations under one Day each keep their own folder and lose their splits,
// rather than being merged into one. Merging is [m]'s job.
//
// Every descendant's ID (and anything already folded into it) is recorded on
// the surviving node so Confirm remaps their files onto its path —
// remapUnderMerged also covers anything below them. FileCount already counts
// the whole subtree, so it doesn't change.
//
// Unlike [d] this works on a top-level row: flattening 2023 leaves the files
// in 2023, not in the library root.
func (m *reviewModel) flattenFolders(targets []*reviewRow) {
	ids := make([]string, 0, len(targets))
	for _, r := range targets {
		if len(r.node.Children) > 0 {
			ids = append(ids, r.node.ID)
		}
	}
	if len(ids) == 0 {
		m.statusMsg, m.statusIsErr = "nothing below the selected folder(s) to flatten", true
		return
	}

	m.snapshot("flatten")
	absorbed, lastName := 0, ""
	for _, id := range ids {
		node := findNodeByID(m.tree, id)
		if node == nil {
			continue
		}
		var absorb func(children []vfs.Node)
		absorb = func(children []vfs.Node) {
			for _, c := range children {
				absorbed++
				node.MergedIDs = append(node.MergedIDs, c.ID)
				node.MergedIDs = append(node.MergedIDs, c.MergedIDs...)
				absorb(c.Children)
			}
		}
		absorb(node.Children)
		node.Children = nil
		lastName = node.Name
	}

	m.reflow()
	m.visualMode = false
	into := fmt.Sprintf("%q", lastName)
	if len(ids) > 1 {
		into = fmt.Sprintf("%d folders", len(ids))
	}
	m.statusMsg, m.statusIsErr = fmt.Sprintf("flattened %d subfolders into %s ([u] to undo)", absorbed, into), false
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

	b.WriteString(m.footer())
	return b.String()
}

// footer is everything below the tree — the rename prompt, a spinner, or the
// status line plus the key help. Its height varies with the terminal width
// (the help wraps), so visibleRows measures it instead of assuming.
func (m reviewModel) footer() string {
	var b strings.Builder
	fmt.Fprintln(&b)
	switch {
	case m.editing:
		fmt.Fprintf(&b, "%s%s█\n", style.Warn.Render("New name: "), m.input)
		for _, s := range m.suggestions {
			fmt.Fprintf(&b, "  %s %s\n", s.name, style.Dim.Render("("+s.detail+")"))
		}
		fmt.Fprintln(&b, m.wrapDim("[enter] apply  [tab] use top match  [ctrl+e] wider search  [esc] cancel"))
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
				fmt.Fprintln(&b, m.wrapDim(m.statusMsg))
			}
		}
		fmt.Fprintln(&b, m.wrapDim(keyHelp))
	}
	return b.String()
}

const keyHelp = "[↑/↓] move  [n/N] next/prev at level  [enter] accept suggestion  [r] rename  [p] peek  [a] accept all  " +
	"[V] select  [m] merge  [d] drop folder  [D] flatten below  [u] undo  [L] layout  [c] save & exit  [q] discard"

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
