package library

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
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

	// a discard, reset or switch holds the library while it removes the draft
	release, ok := s.Work.HoldLibrary()
	if !ok {
		t.Fatal("could not hold the library")
	}
	_, err = fresh.Undo()
	release()
	if !errors.Is(err, ErrBusy) {
		t.Errorf("edit while the library is held = %v, want ErrBusy", err)
	}

	// once closed, another process may copy the library and remove the draft
	s.Close()
	if _, err := fresh.Undo(); !errors.Is(err, ErrStaleReview) {
		t.Errorf("edit after the library closed = %v, want ErrStaleReview", err)
	}
}

// reviewSession is an open session with one planned file and its day folder.
func reviewSession(t *testing.T) (*Session, int64) {
	t.Helper()
	s := testSession(t)
	if err := s.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, s.DB(), 1, "/src", "a.jpg", 1)
	return s, dbtest.SeedEntry(t, s.DB(), 1, "/src/a.jpg", "2024/06_June/03/a.jpg")
}

// A front end reads a review on one goroutine while it edits on another.
func TestReviewReadWhileEditing(t *testing.T) {
	s, day := reviewSession(t)
	r, err := s.Review(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for _, read := range []func(){
		func() {
			for _, n := range r.Tree() {
				_ = n.Name
				for _, c := range n.Children {
					_ = c.Name
				}
			}
		},
		func() { _ = len(r.Edits()) },
	} {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					read()
				}
			}
		})
	}
	for i := range 50 {
		if _, err := r.Apply(vfs.Edit{Op: vfs.OpRename, Node: day, To: string(rune('a' + i%26))}); err != nil {
			t.Fatal(err)
		}
		if i%2 == 1 {
			if _, err := r.Undo(); err != nil {
				t.Fatal(err)
			}
		}
	}
	close(stop)
	readers.Wait()
}

// State, Review and Open from separate goroutines never wait on each other
// for good: Session.mu is never taken under WorkGroup.mu.
func TestSessionCallsNeverDeadlock(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg, err := config.New()
	if err != nil {
		t.Fatal(err)
	}
	// closed below, not in a cleanup: a deadlocked session would hang it
	s := New(cfg, logger.NewNoopLogger(), nil)
	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, s.DB(), 1, "/src", "a.jpg", 1)
	dbtest.SeedEntry(t, s.DB(), 1, "/src/a.jpg", "2024/06_June/03/a.jpg")
	stop := make(chan struct{})
	var callers sync.WaitGroup
	for _, call := range []func(){
		func() { s.State(ctx) },
		func() { s.State(ctx) },
		func() { _, _ = s.Review(ctx, nil) },
		func() { _, _ = s.Review(ctx, nil) },
		func() { _ = s.Open(ctx) },
	} {
		callers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					call()
				}
			}
		})
	}
	time.Sleep(2 * time.Second)
	close(stop)
	finished := make(chan struct{})
	go func() { callers.Wait(); close(finished) }()
	select {
	case <-finished:
		s.Close()
	case <-time.After(10 * time.Second):
		t.Fatal("session calls deadlocked")
	}
}
