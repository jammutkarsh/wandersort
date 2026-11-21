package library

import (
	"context"
	"errors"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/location"
)

// ErrStaleReview refuses an edit to a review built over a plan the library
// has since replaced (a scan, a copy, a settings save, a switch).
var ErrStaleReview = errors.New("the plan changed since Organise opened; open it again")

// Review is one review of the library's plan: what Organise shows and edits.
// Edits go through the session: refused beside a job or a re-plan, and once
// the plan the review was built over is replaced.
type Review struct {
	work  *WorkGroup
	draft *vfs.Draft
	plan  uint64

	// Labels are names confirmed in earlier copies, for rename completion.
	Labels []string
	// Folders are the library's folders already copied into, by path; nil if unread.
	Folders map[string]bool
	// Resolver suggests place names, anchored on the settings' saved places; nil without one.
	Resolver *location.Resolver
}

// Review opens a review of the open library's plan. places supplies the
// location resolver (nil, or an error, disables place suggestions). An empty
// plan is vfs.ErrNothingNew.
func (s *Session) Review(ctx context.Context, places func(context.Context) (*location.Resolver, error)) (*Review, error) {
	done, ok := s.Work.StartOnLibrary()
	if !ok {
		return nil, ErrClosing
	}
	defer done()
	plan := s.Work.Plan()
	d := s.DB()
	if d == nil {
		return nil, ErrNotOpen
	}
	tree, err := vfs.BuildTree(ctx, d)
	if err != nil {
		return nil, err
	}
	if len(tree) == 0 {
		return nil, vfs.ErrNothingNew
	}
	r := &Review{work: &s.Work, plan: plan}
	// opening may rewrite the journal (dropping edits of an older plan), so
	// no copy may be applying it meanwhile
	if err := s.Work.whileIdle(func() (err error) {
		r.draft, err = vfs.OpenDraft(s.OutputDir(), tree)
		return err
	}); err != nil {
		return nil, err
	}
	if r.Folders, err = vfs.LibraryFolders(ctx, d); err != nil {
		s.log.Warn("could not read the library's folders; new ones aren't marked", "error", err)
	}
	r.Labels = vfs.Labels(ctx, d, s.log)
	if places != nil {
		resolver, err := places(ctx)
		if err != nil {
			s.log.Warn("Location resolver unavailable, rename completions disabled", "error", err)
		}
		r.Resolver = resolver.WithAnchors(resolver.BuildAnchors(ctx, s.Settings().SavedPlaces()))
	}
	return r, nil
}

// Plan is WorkGroup.Plan when the review was built.
func (r *Review) Plan() uint64 { return r.plan }

// Stale reports whether the library's plan has moved on since the review was built.
func (r *Review) Stale() bool { return r.work.Plan() != r.plan }

// Tree is the plan with every edit applied.
func (r *Review) Tree() []vfs.Node { return r.draft.Tree() }

// Edits are the edits made so far, in order.
func (r *Review) Edits() []vfs.Edit { return r.draft.Edits() }

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
		return change()
	})
}

// whileIdle runs f with no job or re-plan running, and none able to start
// until f returns; it refuses while one runs. f must be short: it holds the
// group's lock.
func (g *WorkGroup) whileIdle(f func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case g.closed:
		return ErrClosing
	case g.rewrites.Load() > 0:
		return ErrReplanning
	case g.job != "":
		return &BusyError{Job: g.job}
	}
	return f()
}
