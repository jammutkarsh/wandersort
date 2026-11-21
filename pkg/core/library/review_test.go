package library

import (
	"context"
	"errors"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

// A review's edits go through the session: refused while a copy applies the
// draft, and once the plan the review was built over is replaced.
func TestReviewEditsRefusedBesideWork(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, s.DB(), 1, "/src", "a.jpg", 1)
	day := dbtest.SeedEntry(t, s.DB(), 1, "/src/a.jpg", "2024/06_June/03/a.jpg")
	r, err := s.Review(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	rename := vfs.Edit{Op: vfs.OpRename, Node: day, To: "Trip"}

	done, err := s.Work.StartJob(JobCopy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply(rename); !errors.Is(err, ErrBusy) {
		t.Errorf("edit during a copy = %v, want ErrBusy", err)
	}
	if len(r.Edits()) != 0 {
		t.Errorf("a refused edit was kept: %+v", r.Edits())
	}
	done()
	if _, err := r.Apply(rename); !errors.Is(err, ErrStaleReview) {
		t.Errorf("edit after the copy replaced the plan = %v, want ErrStaleReview", err)
	}

	fresh, err := s.Review(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Apply(rename); err != nil {
		t.Errorf("edit on a fresh review: %v", err)
	}
}
