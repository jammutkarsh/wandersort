package vfs

// Review core: the pending plan as a folder tree, edits applied back onto
// folder_nodes and virtual_fs_entries. Nodes match by folder_nodes id.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
)

// ErrInvalidTree wraps every rejection of a submitted review tree (unknown node
// id, unsafe name, colliding rename).
var ErrInvalidTree = errors.New("invalid review tree")

// ErrNoProposal means there is nothing to review: nothing proposed yet, or a
// rescan replaced the proposal mid-review.
var ErrNoProposal = errors.New("no proposal to review")

// Node is one folder in the proposed hierarchy. ID is its folder_nodes id.
type Node struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	FileCount int      `json:"fileCount"`
	Samples   []string `json:"samples,omitempty"`
	Children  []Node   `json:"children"`
	// One exemplar GPS coordinate (location-depth node only), so the review UI
	// can re-query the resolver for ranked rename alternatives.
	Lat *float64 `json:"lat,omitempty"`
	Lon *float64 `json:"lon,omitempty"`
	// IDs of nodes a review merge folded into this one; Confirm moves their
	// files and subfolders here.
	MergedIDs []int64 `json:"mergedIds,omitempty"`
	// What the folder holds, as stored; the edits in edit.go transform it and
	// Confirm writes it back.
	Bounds Bounds `json:"bounds"`
	// Level is the level that made the folder (folder_nodes.level): a Rules
	// name, or one of the Level* constants. Fixed reads it.
	Level string `json:"level,omitempty"`
}

// ErrFixedFolder refuses renaming or removing a year or month folder: new
// files find them by name, so a renamed month would get a twin.
var ErrFixedFolder = errors.New("year and month folders are fixed — new files find them by name")

// Fixed reports whether n is a year or month folder, which the review can't
// rename, merge or drop.
func (n Node) Fixed() bool {
	return n.Level == LevelYear || n.Level == LevelMonth
}

const maxSamples = 3

// BuildTree returns the proposed folder tree for the still-reviewable entries
// of the whole library. Empty means no proposal.
func BuildTree(ctx context.Context, database *db.DB) ([]Node, error) {
	var rows []struct {
		NodeID       int64    `db:"node_id"`
		SourcePath   string   `db:"source_path"`
		LocationNode *int64   `db:"location_node_id"`
		GPSLat       *float64 `db:"exif_gps_latitude"`
		GPSLon       *float64 `db:"exif_gps_longitude"`
	}
	// orphaned sidecars never enter the tree
	if err := database.SQL.SelectContext(ctx, &rows,
		`SELECT vfe.node_id, vfe.source_path, vfe.location_node_id,
		        fm.exif_gps_latitude, fm.exif_gps_longitude
		 FROM virtual_fs_entries vfe
		 JOIN folder_nodes fn ON fn.id = vfe.node_id
		 LEFT JOIN file_metadata fm ON fm.file_id = vfe.file_id
		 WHERE `+db.PendingTransfer("vfe.file_id")+` AND fn.level != ?
		 ORDER BY vfe.id`,
		LevelOrphan); err != nil {
		return nil, fmt.Errorf("query vfs entries: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	folders, err := loadFolderRows(ctx, database.SQL)
	if err != nil {
		return nil, err
	}

	type tnode struct {
		Node
		children []*tnode
	}
	byID := map[int64]*tnode{}
	root := &tnode{}
	// get returns id's tree node, creating it — and linking it under its
	// parent — the first time a file below it is seen
	var get func(id int64) *tnode
	get = func(id int64) *tnode {
		if t, ok := byID[id]; ok {
			return t
		}
		f := folders[id]
		t := &tnode{Node: Node{ID: id, Name: f.Name, Bounds: f.Bounds, Level: f.Level}}
		byID[id] = t
		parent := root
		if f.Parent != 0 {
			parent = get(f.Parent)
		}
		parent.children = append(parent.children, t)
		return t
	}

	for _, r := range rows {
		get(r.NodeID)
		// counts and samples accumulate on every ancestor, so any node a
		// reviewer lands on can report its size and open a preview
		for id := r.NodeID; id != 0; id = folders[id].Parent {
			t := byID[id]
			t.FileCount++
			if len(t.Samples) < maxSamples {
				t.Samples = append(t.Samples, r.SourcePath)
			}
		}
		// GPS attaches to the folder the location level made
		if r.LocationNode == nil || r.GPSLat == nil || r.GPSLon == nil {
			continue
		}
		if loc, ok := byID[*r.LocationNode]; ok && loc.Lat == nil {
			loc.Lat, loc.Lon = r.GPSLat, r.GPSLon
		}
	}

	var finalize func(ts []*tnode) []Node
	finalize = func(ts []*tnode) []Node {
		sort.SliceStable(ts, func(i, j int) bool {
			if ts[i].Name != ts[j].Name {
				return ts[i].Name < ts[j].Name
			}
			return ts[i].ID < ts[j].ID
		})
		out := make([]Node, 0, len(ts))
		for _, t := range ts {
			node := t.Node
			node.Children = finalize(t.children)
			out = append(out, node)
		}
		return out
	}
	return finalize(root.children), nil
}

// FilesUnder returns the source paths of every file proposed under nodeID, in
// a stable order (for the review preview).
func FilesUnder(ctx context.Context, nodeID int64, database *db.DB) ([]string, error) {
	var paths []string
	if err := database.SQL.SelectContext(ctx, &paths, `
		WITH RECURSIVE sub(id) AS (
			SELECT ?
			UNION ALL
			SELECT fn.id FROM folder_nodes fn JOIN sub ON fn.parent_id = sub.id
		)
		SELECT source_path FROM virtual_fs_entries
		WHERE `+db.PendingTransfer("file_id")+` AND node_id IN (SELECT id FROM sub)
		ORDER BY source_path`,
		nodeID); err != nil {
		return nil, fmt.Errorf("query files under folder %d: %w", nodeID, err)
	}
	return paths, nil
}

// Labels returns every folder name typed in an earlier review, for rename
// completions. A failure only warns and returns nothing.
func Labels(ctx context.Context, database *db.DB, log logger.Logger) []string {
	if database == nil {
		return nil
	}
	var labels []string
	if err := database.SQL.SelectContext(ctx, &labels,
		`SELECT DISTINCT label FROM user_labels ORDER BY label`); err != nil {
		if log != nil {
			log.Warn("Could not load confirmed labels for rename suggestions", "error", err)
		}
		return nil
	}
	return labels
}

// Confirm applies the edited tree onto the plan's folders and entries, and
// records typed names in user_labels. Synchronous: nil means committed.
func Confirm(ctx context.Context, database *db.DB, roots []Node) error {
	if err := database.Writer.WriteSync(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
		return confirm(ctx, tx, roots)
	}); err != nil {
		return fmt.Errorf("confirm vfs: %w", err)
	}
	return nil
}

// confirm is Confirm inside the caller's transaction, so a dry run can apply
// the same edits and roll them back.
func confirm(ctx context.Context, tx *sqlx.Tx, roots []Node) error {
	var entryCount int
	if err := tx.GetContext(ctx, &entryCount,
		`SELECT COUNT(*) FROM virtual_fs_entries`); err != nil {
		return fmt.Errorf("count vfs entries: %w", err)
	}
	if entryCount == 0 {
		return ErrNoProposal
	}

	// only still-reviewable rows are moved; executed/failed rows keep
	// the paths they were moved under
	var entries []struct {
		ID         int64  `db:"id"`
		NodeID     int64  `db:"node_id"`
		TargetPath string `db:"target_path"`
		SourcePath string `db:"source_path"`
	}
	if err := tx.SelectContext(ctx, &entries,
		`SELECT id, node_id, target_path, source_path FROM virtual_fs_entries WHERE `+
			db.PendingTransfer("file_id")+` ORDER BY id`); err != nil {
		return err
	}
	// a rescan replaces the proposal set wholesale; if it won the race the
	// rows are gone and this confirm must fail, not half-apply
	if len(entries) == 0 {
		return fmt.Errorf("%w: proposal was replaced by a newer scan", ErrNoProposal)
	}

	twins, err := splitPlacedFolders(ctx, tx)
	if err != nil {
		return err
	}
	folders, err := loadFolderRows(ctx, tx)
	if err != nil {
		return err
	}
	edits, err := readTree(remapIDs(roots, twins), folders)
	if err != nil {
		return err
	}
	if err := edits.apply(ctx, tx); err != nil {
		return err
	}
	if folders, err = loadFolderRows(ctx, tx); err != nil {
		return err
	}
	dirs := map[int64]string{}

	// Moved folders can land two files on one name: unmoved rows and placed
	// files keep theirs, moved rows take the next _N, one per capture group.
	//
	// ponytail: groups are rebuilt from source dir + captureStem (no time
	// window) and numbered in row order, not capture time. Stable, but a pair
	// split across folders stays matched only when both move. Persist the
	// pair key if that bites.
	placed, err := placedPaths(ctx, tx)
	if err != nil {
		return err
	}
	taken := make(map[string]bool, len(placed)+len(entries))
	for _, p := range placed {
		taken[nameKey(p)] = true
	}
	type move struct {
		id   int64
		dir  string
		base string
	}
	groups := map[string][]move{}
	var keys []string
	for _, e := range entries {
		// split and merged-away folders' entries were moved above
		nodeID := e.NodeID
		if twin, ok := twins[nodeID]; ok {
			nodeID = twin
		}
		nodeID = edits.survivor(nodeID)
		newDir := folderPath(folders, dirs, nodeID)
		if newDir == path.Dir(e.TargetPath) {
			taken[nameKey(e.TargetPath)] = true
			continue
		}
		key := path.Dir(e.SourcePath) + "|" + captureStem(path.Base(e.SourcePath))
		if groups[key] == nil {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], move{e.ID, newDir, path.Base(e.TargetPath)})
	}
	for _, key := range keys {
		members := groups[key]
		paths := make([]string, len(members))
		assignSuffix(taken, paths, func(k int) (string, string) {
			return members[k].dir, members[k].base
		})
		for k, mv := range members {
			if _, err := tx.ExecContext(ctx,
				`UPDATE virtual_fs_entries SET target_path = ? WHERE id = ?`,
				paths[k], mv.id); err != nil {
				return err
			}
		}
	}
	for _, name := range edits.learned {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_labels (label, kind) VALUES (?, 'EVENT') ON CONFLICT DO NOTHING`, name); err != nil {
			return err
		}
	}
	return nil
}

// treeEdits is a submitted review tree read against the stored folders: where
// each folder now sits, what it holds, which folders were folded into which,
// and the names the reviewer typed.
type treeEdits struct {
	placeOf    map[int64]folderKey
	bounds     map[int64]Bounds
	mergedInto map[int64]int64
	learned    []string
}

// readTree validates roots against folders and turns them into treeEdits. Two
// siblings given the same name merge into the first.
func readTree(roots []Node, folders map[int64]folderRow) (treeEdits, error) {
	e := treeEdits{placeOf: map[int64]folderKey{}, bounds: map[int64]Bounds{}, mergedInto: map[int64]int64{}}
	seen := map[folderKey]int64{}
	learned := map[string]bool{}
	var walk func(nodes []Node, parent int64) error
	walk = func(nodes []Node, parent int64) error {
		for _, n := range nodes {
			name := strings.TrimSpace(n.Name)
			if name == "" || name == "." || name == ".." {
				return fmt.Errorf("%w: invalid node name %q", ErrInvalidTree, n.Name)
			}
			// NFC like every other folder name, so the collision check and
			// the stored name compare the same spelling
			name = wspath.ToLibrary(wspath.SanitizeSegment(name))
			for _, id := range append([]int64{n.ID}, n.MergedIDs...) {
				if _, ok := folders[id]; !ok {
					return fmt.Errorf("%w: unknown node id %d", ErrInvalidTree, id)
				}
			}
			// a moved-but-unrenamed folder keeps the planner's name; only
			// typed names are remembered
			if name != folders[n.ID].Name {
				learned[name] = true
			}
			id := n.ID
			k := folderKey{parent, name}
			if twin, ok := seen[k]; ok {
				// twin == id when a split mapped two tree nodes onto one folder
				if twin != id {
					e.mergedInto[id] = twin
				}
				id = twin
				// the same merge rule mergeNodes applies, since this is one
				e.bounds[id] = e.bounds[id].Union(n.Bounds)
			} else {
				seen[k] = id
				e.placeOf[id] = k
				e.bounds[id] = n.Bounds
			}
			for _, m := range n.MergedIDs {
				if m != id {
					e.mergedInto[m] = id
				}
			}
			if err := walk(n.Children, id); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(roots, 0); err != nil {
		return treeEdits{}, err
	}
	e.learned = slices.Sorted(maps.Keys(learned))
	return e, nil
}

// remapIDs returns a copy of roots with every node and merged ID in twins
// swapped for its twin, so edits made on a split folder land on the new one.
func remapIDs(roots []Node, twins map[int64]int64) []Node {
	if len(twins) == 0 {
		return roots
	}
	out := cloneTree(roots)
	var walk func(nodes []Node)
	walk = func(nodes []Node) {
		for i := range nodes {
			if twin, ok := twins[nodes[i].ID]; ok {
				nodes[i].ID = twin
			}
			for j, m := range nodes[i].MergedIDs {
				if twin, ok := twins[m]; ok {
					nodes[i].MergedIDs[j] = twin
				}
			}
			walk(nodes[i].Children)
		}
	}
	walk(out)
	return out
}

// survivor is the folder id's files live in once the edits are applied.
func (e treeEdits) survivor(id int64) int64 {
	if into, ok := e.mergedInto[id]; ok {
		return into
	}
	return id
}

// apply writes the edits: folded folders' files and subfolders move to their
// survivor first, then every folder takes its edited parent, name and bounds,
// then empty folders are deleted.
func (e treeEdits) apply(ctx context.Context, tx *sqlx.Tx) error {
	for _, m := range slices.Sorted(maps.Keys(e.mergedInto)) {
		into := e.mergedInto[m]
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_fs_entries SET node_id = ? WHERE node_id = ? AND `+db.PendingTransfer("file_id"),
			into, m); err != nil {
			return fmt.Errorf("move files of folder %d: %w", m, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE virtual_fs_entries SET location_node_id = ? WHERE location_node_id = ? AND `+db.PendingTransfer("file_id"),
			into, m); err != nil {
			return fmt.Errorf("move location of folder %d: %w", m, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE folder_nodes SET parent_id = ? WHERE parent_id = ?`, into, m); err != nil {
			return fmt.Errorf("move subfolders of folder %d: %w", m, err)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(e.placeOf)) {
		k := e.placeOf[id]
		if _, err := tx.ExecContext(ctx,
			`UPDATE folder_nodes SET parent_id = ?, name = ?, bounds = ? WHERE id = ?`,
			nullableID(k.parent), k.name, e.bounds[id], id); err != nil {
			return fmt.Errorf("place folder %d: %w", id, err)
		}
	}
	return pruneFolders(ctx, tx)
}
