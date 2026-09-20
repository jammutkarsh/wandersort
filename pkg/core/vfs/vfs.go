// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package vfs is the pipeline's final phase: it proposes a destination folder
// hierarchy for every master file in the library, persisted as rows in
// virtual_fs_entries, without touching anything on disk. The review flow
// corrects it; the Execute phase performs the copy/move.
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

// Propose builds the proposal for the whole library from the user's settings —
// the phase as a single call, for every caller that has an *config.Configuration
// and a resolver (the scan pipeline, and cli's rebuildTree). Assembling the
// Config and resolving the saved-place anchors are steps of the phase, not of
// its callers; New is for a test or a caller that wants to state the Config
// itself.
func Propose(ctx context.Context, database *db.DB, resolver *location.Resolver, appCfg *config.Configuration, log logger.Logger) (int, error) {
	// A new proposal means new folder IDs, so review edits made against the old
	// one mean nothing now (spec D19, D20): a scan and a settings re-plan both
	// discard them here, before anything is replaced.
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

// Run builds the virtual filesystem proposal for the whole library's master
// files
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

// placedPaths is every library-relative path a placed file already holds. Both
// planners that hand out names (buildTargets, Confirm) seed from it, so a new
// file never takes one.
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

// placedTimes is the capture time of every placed file, for the clustering to
// read (spec D16). Undated files are left out: they join no cluster.
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

// loadMasters reads every live, not-yet-placed master in the library with its
// hashed metadata. A placed file is never re-proposed — its row is already
// the plan, and persist's kept-row logic leaves it alone — so there
// is nothing here for it to win or lose. Not session-scoped: the proposal
// must cover earlier sessions' files too, or the output would depend on scan
// history. Ordered by (file_dir, file_name), not id, so clustering and
// collision suffixes don't vary with worker order.
func (v *VFS) loadMasters(ctx context.Context) ([]masterFile, error) {
	var masters []masterFile
	if err := v.db.SQL.SelectContext(ctx, &masters, masterColumns+`
		WHERE fm.is_master = 1 AND fr.placed = 0
		ORDER BY fr.file_dir, fr.file_name`); err != nil {
		return nil, fmt.Errorf("query master files: %w", err)
	}
	for i := range masters {
		masters[i].absPath = filepath.Join(masters[i].FileDir, masters[i].FileName)
	}
	return masters, nil
}

// persist replaces the pending part of the library's plan, and leaves every
// row a transfer already decided alone (its file placed, or a TRANSFER error
// recorded): a rebuild re-proposes what has not happened yet, not what has. A
// kept row whose file is no longer a live master goes, or the plan would keep
// promising to move a file that isn't there.
//
// One synchronous transaction: every caller reads the rows straight back (the
// review rebuilds its tree the moment Propose returns), and the folder rows
// the entries point at have to exist before the entries do.
func (v *VFS) persist(ctx context.Context, masters []masterFile) (int, error) {
	err := v.db.Writer.WriteSync(func(ctx context.Context, tx *sqlx.Tx) error {
		// A decided row is one whose file is placed or failed to transfer; that
		// file must not be proposed a second time — UNIQUE(file_id) says so too.
		// Same "protected" set as the delete below (is_master = 1 OR placed =
		// 1): a placed file is never in masters (loadMasters filters it out),
		// so this never actually keeps one in practice, but the two queries
		// describing one invariant should read the same rather than drift apart.
		var keptIDs []int64
		if err := tx.SelectContext(ctx, &keptIDs, `
			SELECT file_id FROM virtual_fs_entries
			WHERE NOT `+db.PendingTransfer("file_id")+` AND file_id IN (
				SELECT fr.id FROM file_registry fr
				JOIN file_metadata fm ON fm.file_id = fr.id
				WHERE fm.is_master = 1 OR fr.placed = 1)`); err != nil {
			return fmt.Errorf("load decided vfs entries: %w", err)
		}
		kept := make(map[int64]bool, len(keptIDs))
		for _, id := range keptIDs {
			kept[id] = true
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM virtual_fs_entries WHERE `+db.PendingTransfer("file_id")); err != nil {
			return fmt.Errorf("clear previous vfs proposal: %w", err)
		}
		// a decided row for a file that is no longer a live master promises a
		// move that can't happen. A placed file is exempt regardless of
		// is_master: it already landed, so its row is the one true record of
		// that (spec D10) — the scorer never demotes a placed file's own hash
		// group (see scorer.Run), but this is the backstop if it ever did.
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM virtual_fs_entries
			WHERE NOT `+db.PendingTransfer("file_id")+` AND file_id NOT IN (
				SELECT fr.id FROM file_registry fr
				JOIN file_metadata fm ON fm.file_id = fr.id
				WHERE fm.is_master = 1 OR fr.placed = 1)`); err != nil {
			return fmt.Errorf("clear stale vfs entries: %w", err)
		}

		// files a stopped copy left beside copied ones move to their own chain
		// first, so the new plan finds that chain by name and joins it instead
		// of showing a same-named twin next to it until the next review save
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

// insertChunk is how many proposals go into one INSERT. A long VALUES list is
// expensive for SQLite to compile, so this is a measured trough over 20k rows
// (1 row 504ms, 50 rows 241ms, 500 rows 486ms), not "bigger is better".
const insertChunk = 50

// insertStatement builds one parameterised multi-row INSERT for a chunk, so
// SQLite compiles the statement once instead of once per proposal. Masters in
// kept already hold a decided entry and are skipped; n is how many rows the
// statement actually inserts (0 = nothing to run).
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
