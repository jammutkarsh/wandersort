// Package vfs proposes a destination folder hierarchy for every master file,
// persisted in folder_nodes and virtual_fs_entries. Nothing on disk is touched.
package vfs

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	wspath "github.com/jammutkarsh/wandersort/pkg/path"
)

type VFS struct {
	db       *db.DB
	resolver *location.Resolver
	log      logger.Logger
	cfg      Config
}

func New(db *db.DB, resolver *location.Resolver, log logger.Logger, cfg Config) *VFS {
	return &VFS{
		db:       db,
		resolver: resolver,
		log:      log,
		cfg:      cfg,
	}
}

// Propose builds the proposal for the whole library from the library's
// settings: config, anchors, then Run.
func Propose(ctx context.Context, database *db.DB, resolver *location.Resolver, appCfg *config.Configuration, log logger.Logger) (int, error) {
	// a new proposal has new folder IDs, so old review edits are void
	if appCfg.AppDBPath != "" {
		if err := RemoveDraft(filepath.Dir(appCfg.AppDBPath)); err != nil {
			return 0, err
		}
	}
	cfg := ConfigFor(appCfg)
	cfg.Anchors = resolver.BuildAnchors(ctx, appCfg.SavedPlaces)
	log.Info("Proposing destination folders", "rules", cfg.Rules, "anchors", len(cfg.Anchors))
	return New(database, resolver, log, cfg).Run(ctx)
}

// Run builds and persists the proposal for every master in the library.
func (v *VFS) Run(ctx context.Context) (int, error) {
	v.log.Info("Building virtual filesystem")

	masters, err := v.loadMasters(ctx)
	if err != nil {
		return 0, err
	}
	if len(masters) == 0 {
		v.log.Info("No master files to organize")
		return 0, nil
	}

	cfg := v.cfg
	if cfg.Placed, err = placedPaths(ctx, v.db.SQL); err != nil {
		return 0, err
	}
	var tree []folderRow
	if err := v.db.SQL.SelectContext(ctx, &tree, placedFoldersCTE+`
		SELECT id, COALESCE(parent_id, 0) AS parent_id, name, level, bounds FROM folder_nodes
		WHERE id IN (SELECT id FROM placed_folders)`); err != nil {
		return 0, fmt.Errorf("load placed folders: %w", err)
	}
	cfg.placedTree = newPlacedTree(tree)
	if cfg.placedTimes, err = v.placedTimes(ctx); err != nil {
		return 0, err
	}

	if err := Plan(ctx, masters, cfg, v.resolver, v.log); err != nil {
		return 0, err
	}

	count, err := v.persist(ctx, masters)
	if err != nil {
		return count, err
	}

	v.log.Info("Virtual filesystem proposed", "entries", count)
	return count, nil
}

// placedPaths is every library-relative path a placed file holds; new names
// never take one.
func placedPaths(ctx context.Context, q sqlx.QueryerContext) ([]string, error) {
	var paths []string
	if err := sqlx.SelectContext(ctx, q, &paths, `
		SELECT vfe.target_path FROM virtual_fs_entries vfe
		JOIN file_registry fr ON fr.id = vfe.file_id
		WHERE fr.placed = 1`); err != nil {
		return nil, fmt.Errorf("load placed paths: %w", err)
	}
	return paths, nil
}

// masterColumns is what loadMasters and placedTimes read of a file.
const masterColumns = `
	SELECT fr.id, fr.file_dir, fr.file_name, fm.file_hash, fr.media_type, fr.file_extension, fr.file_modified_at,
		fm.exif_image_width, fm.exif_image_height, fm.exif_orientation,
		fm.exif_gps_latitude, fm.exif_gps_longitude,
		fm.exif_make, fm.exif_model, fm.exif_date_time_original, fm.exif_create_date,
		fm.exif_creation_date, fm.exif_media_create_date, fm.is_screenshot
	FROM file_registry fr
	JOIN file_metadata fm ON fm.file_id = fr.id`

// placedTimes is the capture time of every dated placed file.
func (v *VFS) placedTimes(ctx context.Context) ([]time.Time, error) {
	var placed []masterFile
	if err := v.db.SQL.SelectContext(ctx, &placed, masterColumns+` WHERE fr.placed = 1`); err != nil {
		return nil, fmt.Errorf("query placed files: %w", err)
	}
	times := make([]time.Time, 0, len(placed))
	for i := range placed {
		if t := placed[i].captureTime(); !t.IsZero() {
			times = append(times, t)
		}
	}
	return times, nil
}

// loadMasters reads every live, unplaced file with its metadata and elects one
// copy per hash. A hash group with a placed member is dropped in SQL: the
// placed file is that hash's master, so a re-imported card isn't copied again.
// Ordered by (file_dir, file_name) so election, clustering and suffixes are
// deterministic.
func (v *VFS) loadMasters(ctx context.Context) ([]masterFile, error) {
	var rows []masterFile
	if err := v.db.SQL.SelectContext(ctx, &rows, masterColumns+`
		WHERE fr.placed = 0 AND fm.file_hash NOT IN (
			SELECT fm2.file_hash FROM file_metadata fm2
			JOIN file_registry fr2 ON fr2.id = fm2.file_id
			WHERE fr2.placed = 1)
		ORDER BY fr.file_dir, fr.file_name`); err != nil {
		return nil, fmt.Errorf("query master files: %w", err)
	}
	for i := range rows {
		rows[i].absPath = filepath.Join(rows[i].FileDir, rows[i].FileName)
	}
	masters, groups := electMasters(rows)
	if groups > 0 {
		v.log.Info(fmt.Sprintf("Kept the best copy of %d duplicate group(s)", groups), logger.UserKey, true)
	}
	return masters, nil
}

// persist replaces the pending part of the plan in one synchronous
// transaction and leaves decided rows (placed, or TRANSFER error) alone, except
// a failed row whose file is no longer an elected master.
func (v *VFS) persist(ctx context.Context, masters []masterFile) (int, error) {
	err := v.db.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		// A placed file's row is kept unconditionally. A failed row stays only
		// while its file is still the elected master of its hash.
		var failedIDs []int64
		if err := tx.SelectContext(ctx, &failedIDs, `
			SELECT file_id FROM virtual_fs_entries
			WHERE NOT `+db.PendingTransfer("file_id")+`
			AND file_id NOT IN (SELECT id FROM file_registry WHERE placed = 1)`); err != nil {
			return fmt.Errorf("load failed vfs entries: %w", err)
		}
		elected := make(map[int64]bool, len(masters))
		for i := range masters {
			elected[masters[i].FileID] = true
		}
		var stale []int64
		for _, id := range failedIDs {
			if !elected[id] {
				stale = append(stale, id)
			}
		}

		var keptIDs []int64
		if err := tx.SelectContext(ctx, &keptIDs, `
			SELECT file_id FROM virtual_fs_entries
			WHERE NOT `+db.PendingTransfer("file_id")); err != nil {
			return fmt.Errorf("load decided vfs entries: %w", err)
		}
		kept := make(map[int64]bool, len(keptIDs))
		for _, id := range keptIDs {
			kept[id] = true
		}
		for _, id := range stale {
			delete(kept, id)
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM virtual_fs_entries WHERE `+db.PendingTransfer("file_id")); err != nil {
			return fmt.Errorf("clear previous vfs proposal: %w", err)
		}
		if err := db.ExecIn(ctx, tx, `DELETE FROM virtual_fs_entries WHERE file_id IN (?)`, stale); err != nil {
			return fmt.Errorf("clear stale vfs entries: %w", err)
		}

		// split folders a stopped copy shares with placed files first, so the
		// new plan joins that chain instead of showing a twin
		if _, err := splitPlacedFolders(ctx, tx); err != nil {
			return err
		}
		folders, err := loadFolders(ctx, tx)
		if err != nil {
			return err
		}
		// folders still holding a decided file keep what they already hold on
		// top of what this plan adds; the rest mean only this plan's files
		occupied := map[int64]Bounds{}
		var used []folderRow
		if err := tx.SelectContext(ctx, &used, usedFoldersCTE+`
			SELECT id, bounds FROM folder_nodes WHERE id IN (SELECT id FROM used)`); err != nil {
			return fmt.Errorf("load occupied folders: %w", err)
		}
		for _, r := range used {
			occupied[r.ID] = r.Bounds
		}
		bounds := map[int64]Bounds{}
		for i := range masters {
			m := &masters[i]
			if kept[m.FileID] {
				continue
			}
			chain, err := folders.ensure(ctx, tx, path.Dir(wspath.ToLibrary(m.targetPath)), m.dirLevels)
			if err != nil {
				return err
			}
			m.nodeID, m.locationNodeID = chain[len(chain)-1], 0
			if d := m.locationDepth(); d >= 0 && d < len(chain) {
				m.locationNodeID = chain[d]
			}
			for d, id := range chain {
				c := Bounds{{}}
				if d < len(m.dirBounds) {
					c = m.dirBounds[d]
				}
				b, ok := bounds[id]
				if !ok {
					b = occupied[id] // nil unless the folder holds a decided file
				}
				bounds[id] = b.Union(c)
			}
		}
		// rewritten on every plan, reused folders included: a range folder a
		// new day joined must say so
		for id, b := range bounds {
			if _, err := tx.ExecContext(ctx, `UPDATE folder_nodes SET bounds = ? WHERE id = ?`, b, id); err != nil {
				return fmt.Errorf("store folder bounds: %w", err)
			}
		}

		for chunk := range slices.Chunk(masters, insertChunk) {
			stmt, args, n := insertStatement(chunk, kept)
			if n == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
				return fmt.Errorf("persist %d vfs entries from file %d: %w", n, chunk[0].FileID, err)
			}
		}
		return pruneFolders(ctx, tx)
	})
	if err != nil {
		return 0, fmt.Errorf("persist vfs proposal: %w", err)
	}
	// every live master now has exactly one entry: freshly proposed, or kept
	return len(masters), nil
}

// insertChunk is rows per INSERT; measured on 20k rows (1: 504ms, 50: 241ms,
// 500: 486ms).
const insertChunk = 50

// insertStatement builds one multi-row INSERT for a chunk, skipping masters
// in kept. n is the rows it inserts (0 = nothing to run).
func insertStatement(chunk []masterFile, kept map[int64]bool) (stmt string, args []any, n int) {
	var b strings.Builder
	b.WriteString(`INSERT INTO virtual_fs_entries
		(file_id, source_path, node_id, target_path, cluster_id, location_node_id)
		VALUES `)
	args = make([]any, 0, len(chunk)*6)
	for i := range chunk {
		m := &chunk[i]
		if kept[m.FileID] {
			continue
		}
		if n > 0 {
			b.WriteString(",")
		}
		b.WriteString("(?,?,?,?,?,?)")
		args = append(args, m.FileID, wspath.ToSourcePath(m.absPath), m.nodeID, wspath.ToLibrary(m.targetPath),
			nullable(m.clusterID), nullableID(m.locationNodeID))
		n++
	}
	return b.String(), args, n
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
