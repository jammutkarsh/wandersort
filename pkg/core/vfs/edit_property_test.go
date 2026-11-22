package vfs

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"path"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/install/installtest"
)

// Whatever edits are made, the review shows every file once, in the folder the
// copy puts it in: each planned file's folder is one tree node (itself or
// folded into it), counts add up, and no folder is shown twice.
func TestRandomEditsCopyWhatTheReviewShows(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	places := [][2]float64{{0, 0}, {48.8566, 2.3522}, {41.9028, 12.4964}, {48.8049, 2.1204}}
	n := 0
	for _, month := range []int{5, 6, 7} {
		for _, day := range []int{1, 2, 3, 10} {
			for i, p := range places {
				if (month+day+i)%3 == 0 {
					continue // some days all in one place, some without any
				}
				n++
				h.addFile(t, fmt.Sprintf("dump/IMG_%03d.JPG", n), "IMAGE",
					metaWith(fmt.Sprintf("2024:%02d:%02d %02d:00:00", month, day, 8+i), p[0], p[1], 3024, 4032))
			}
		}
	}
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDate, RuleLocation}
	h.build(t, cfg, installtest.Resolver(t))
	base, err := BuildTree(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		ID   int64 `db:"id"`
		Node int64 `db:"node_id"`
	}
	if err := h.d.SQL.Select(&entries, `SELECT id, node_id FROM virtual_fs_entries`); err != nil {
		t.Fatal(err)
	}

	// shown maps each stored folder to where the review shows its files
	shown := func(tree []Node) (out map[int64]string, err error) {
		out = map[int64]string{}
		var walk func(nodes []Node, dir string)
		walk = func(nodes []Node, dir string) {
			names := map[string]bool{}
			for _, node := range nodes {
				at := path.Join(dir, node.Name)
				if names[nameKey(node.Name)] {
					err = fmt.Errorf("two folders at %q", at)
				}
				names[nameKey(node.Name)] = true
				for _, id := range append([]int64{node.ID}, node.MergedIDs...) {
					if other, twice := out[id]; twice {
						err = fmt.Errorf("folder %d is shown at %q and %q", id, other, at)
					}
					out[id] = at
				}
				walk(node.Children, at)
			}
		}
		walk(tree, "")
		return out, err
	}
	own := map[int64]int{}
	for _, e := range entries {
		own[e.Node]++
	}
	// counts checks every folder's count is its own files and its subfolders'
	counts := func(tree []Node) (err error) {
		var walk func(nodes []Node) int
		walk = func(nodes []Node) (total int) {
			for _, node := range nodes {
				holds := walk(node.Children)
				for _, id := range append([]int64{node.ID}, node.MergedIDs...) {
					holds += own[id]
				}
				if node.FileCount != holds {
					err = fmt.Errorf("%s shows %d files, holds %d", node.Name, node.FileCount, holds)
				}
				total += holds
			}
			return total
		}
		if total := walk(tree); total != len(entries) {
			err = fmt.Errorf("the review shows %d files of %d", total, len(entries))
		}
		return err
	}

	for seed := range uint64(200) {
		rng := rand.New(rand.NewPCG(seed, 7))
		tree := cloneTree(base)
		var log []Edit
		for range 10 {
			byDepth := map[int][]int64{}
			var walk func(nodes []Node, depth int)
			walk = func(nodes []Node, depth int) {
				for _, node := range nodes {
					byDepth[depth] = append(byDepth[depth], node.ID)
					walk(node.Children, depth+1)
				}
			}
			walk(tree, 0)
			ids := byDepth[2+rng.IntN(2)] // days and places
			if len(ids) == 0 {
				continue
			}
			rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
			e := Edit{Nodes: ids[:min(len(ids), 1+rng.IntN(3))]}
			switch rng.IntN(4) {
			case 0:
				e = Edit{Op: OpRename, Node: ids[0], To: fmt.Sprintf("Name %d", rng.IntN(4))}
			case 1:
				e.Op = OpMerge
			case 2:
				e.Op = OpDrop
			default:
				e.Op = OpFlatten
			}
			next, _, err := applyEdit(cloneTree(tree), e)
			if err != nil {
				continue
			}
			sortTree(next)
			tree, log = next, append(log, e)
			_, err = shown(tree)
			if err := errors.Join(err, counts(tree)); err != nil {
				t.Fatalf("seed %d: %v\nedits: %+v", seed, err, log)
			}
		}

		where, err := shown(tree)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.d.Writer.DryRun(ctx, func(ctx context.Context, tx *sqlx.Tx) error {
			if err := confirm(ctx, tx, tree); err != nil {
				return err
			}
			for _, e := range entries {
				var target string
				if err := tx.GetContext(ctx, &target, `SELECT target_path FROM virtual_fs_entries WHERE id = ?`, e.ID); err != nil {
					return err
				}
				if got, want := path.Dir(target), where[e.Node]; got != want {
					return fmt.Errorf("seed %d: the copy puts a file in %q, the review showed %q\nedits: %+v", seed, got, want, log)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
