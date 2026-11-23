package library

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/preview"
)

// ErrStaleReview refuses an edit to a review built over a plan the library
// has since replaced (a scan, a copy, a settings save, a switch).
var ErrStaleReview = errors.New("the plan changed since Organise opened; open it again")

// Review is one review of the library's plan: what Organise shows and edits.
// Edits go through the session: refused beside a job or a re-plan, and once
// the plan the review was built over is replaced.
type Review struct {
	s       *Session
	work    *WorkGroup
	draft   *vfs.Draft
	plan    uint64
	library string // the library folder it was built over, keying its peeks

	// mu guards tree and edits: copies of the draft's, replaced whole after
	// each edit, so a reader on another goroutine never sees one half made.
	mu    sync.Mutex
	tree  []vfs.Node
	edits []vfs.Edit

	// Labels are names confirmed in earlier copies, for rename completion.
	Labels []string
	// Folders are the library's folders already copied into, by path; nil if unread.
	Folders map[string]bool
	// Resolver suggests place names, anchored on the settings' saved places; nil without one.
	Resolver *location.Resolver
	// Orphans is how many sidecars the copy takes to the orphan folder, outside the tree.
	Orphans int
}

// Review is the review of the library's plan, opening the library first: one per plan, shared by
// every caller, since two would each rewrite the journal from their own edits.
// A review whose plan moved on is replaced. places supplies the location
// resolver when one is built (nil, or an error, disables place suggestions);
// it is waited for before the library is held. An empty plan is
// vfs.ErrNothingNew.
func (s *Session) Review(ctx context.Context, places func(context.Context) (*location.Resolver, error)) (*Review, error) {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	if r := s.review; r != nil && !r.Stale() {
		return r, nil
	}
	var resolver *location.Resolver
	if places != nil {
		var err error
		if resolver, err = places(ctx); err != nil {
			s.log.Warn("Location resolver unavailable, rename completions disabled", "error", err)
		}
	}

	done, err := s.Work.StartOnLibrary()
	if err != nil {
		return nil, err
	}
	defer done()
	if err := s.openExisting(ctx); err != nil {
		return nil, err
	}
	// read with the idle check: a job that ends while the tree is read
	// would otherwise leave this review fresh over the plan it replaced
	plan, err := s.Work.idlePlan()
	if err != nil {
		return nil, err
	}
	d := s.DB()
	tree, err := vfs.BuildTree(ctx, d)
	if err != nil {
		return nil, err
	}
	if len(tree) == 0 {
		return nil, vfs.ErrNothingNew
	}
	// read before whileIdle: Session.mu is never taken under WorkGroup.mu
	dir := s.OutputDir()
	r := &Review{s: s, work: &s.Work, plan: plan, library: dir}
	// opening may rewrite the journal (dropping edits of an older plan), so
	// no copy may be applying it meanwhile
	if err := s.Work.whileIdle(func() (err error) {
		if s.Work.Plan() != plan {
			return ErrStaleReview
		}
		if r.draft, err = vfs.OpenDraft(dir, tree); err != nil {
			return err
		}
		r.tree, r.edits = r.draft.Tree(), r.draft.Edits()
		return nil
	}); err != nil {
		return nil, err
	}
	if r.Folders, err = vfs.LibraryFolders(ctx, d); err != nil {
		s.log.Warn("could not read the library's folders; new ones aren't marked", "error", err)
	}
	if r.Orphans, err = vfs.Orphans(ctx, d); err != nil {
		s.log.Warn("could not count the unpaired sidecars", "error", err)
	}
	r.Labels = vfs.Labels(ctx, d, s.log)
	r.Resolver = resolver.WithAnchors(resolver.BuildAnchors(ctx, s.Settings().SavedPlaces()))
	s.review = r
	return r, nil
}

// Peek copies a folder's files into its preview copy (see preview.Copy) and
// returns that copy's folder for a viewer. The files are read from this
// review's library while it can't be switched; once the plan moved on
// (another library, a copy) the folder may not exist, so it is ErrStaleReview.
func (r *Review) Peek(ctx context.Context, node vfs.Node) (string, error) {
	ctx, cancel := r.s.bound(ctx)
	defer cancel()
	files, err := r.nodeFiles(ctx, node)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no files under %s", node.Name)
	}
	return preview.Copy(ctx, r.library, files)
}

// nodeFiles is node's files, read under the library.
func (r *Review) nodeFiles(ctx context.Context, node vfs.Node) ([]string, error) {
	done, err := r.work.StartOnLibrary()
	if err != nil {
		return nil, err
	}
	defer done()
	d := r.s.DB()
	if r.Stale() || d == nil {
		return nil, ErrStaleReview
	}
	return vfs.NodeFiles(ctx, d, node)
}

// Plan is WorkGroup.Plan when the review was built.
func (r *Review) Plan() uint64 { return r.plan }

// Stale reports whether the library's plan has moved on since the review was built.
func (r *Review) Stale() bool { return r.work.Plan() != r.plan }

// Tree is the plan with every edit applied. It is shared: read it, don't change it.
func (r *Review) Tree() []vfs.Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tree
}

// Edits are the edits made so far, in order. It is shared: read it, don't change it.
func (r *Review) Edits() []vfs.Edit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.edits
}

// Apply makes one edit and journals it (see vfs.Draft.Apply).
func (r *Review) Apply(e vfs.Edit) (out vfs.Outcome, err error) {
	err = r.edit(func() (err error) {
		out, err = r.draft.Apply(e)
		return err
	})
	return out, err
}

// Undo drops the last edit (see vfs.Draft.Undo).
func (r *Review) Undo() (last vfs.Edit, err error) {
	err = r.edit(func() (err error) {
		last, err = r.draft.Undo()
		return err
	})
	return last, err
}

// Reset throws every edit away (see vfs.Draft.Reset).
func (r *Review) Reset() error { return r.edit(r.draft.Reset) }

// edit runs one change to the draft while no job or re-plan can start, and
// only on the plan the review was built over.
func (r *Review) edit(change func() error) error {
	return r.work.whileIdle(func() error {
		if r.work.Plan() != r.plan {
			return ErrStaleReview
		}
		err := change()
		// a failed change may still have rebuilt the draft's tree
		r.mu.Lock()
		r.tree, r.edits = r.draft.Tree(), r.draft.Edits()
		r.mu.Unlock()
		return err
	})
}

// idlePlan is Plan, refused as whileIdle refuses. A job that started before
// it is refused; one that starts after it moves Plan on.
func (g *WorkGroup) idlePlan() (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.idleRefusal(); err != nil {
		return 0, err
	}
	return g.plan.Load(), nil
}

// idleRefusal is why a review may not read or edit the plan now (shutdown,
// a re-plan, a job), nil when it may; called under mu.
func (g *WorkGroup) idleRefusal() error {
	switch {
	case g.closed:
		return ErrClosing
	case g.rewrites.Load() > 0:
		return ErrReplanning
	case g.job != "":
		return &BusyError{Job: g.job}
	}
	return nil
}

// whileIdle runs f with no job or re-plan running, nothing holding the
// library (a discard, reset, switch or close), and none able to start until f
// returns; it refuses while one runs. f must be short and must not take
// Session.mu: it holds the group's lock.
func (g *WorkGroup) whileIdle(f func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.idleRefusal(); err != nil {
		return err
	}
	// never waits, so it can't deadlock under mu; a hold taken after this
	// waits for f to return
	if !g.library.TryRLock() {
		if len(g.holds) > 0 {
			return &BusyError{Job: g.holds[0]}
		}
		return ErrBusy // closing
	}
	defer g.library.RUnlock()
	return f()
}
