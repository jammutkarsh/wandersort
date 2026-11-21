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
	"github.com/jammutkarsh/wandersort/pkg/location"
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

// Every caller shares one review of a plan: two would each rewrite the
// journal from their own edits, and one's edits would vanish.
func TestReviewSharedPerPlan(t *testing.T) {
	ctx := context.Background()
	s, _ := reviewSession(t)
	a, err := s.Review(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Review(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("two reviews of one plan")
	}
	done, err := s.Work.StartJob(JobCopy)
	if err != nil {
		t.Fatal(err)
	}
	done()
	c, err := s.Review(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c == a {
		t.Error("a review of a replaced plan was handed out again")
	}
}

// A review waits for the location database before it holds the library, and
// shutdown stops the wait: a download never keeps a switch or a quit waiting.
func TestReviewWaitsForPlacesOffTheLibrary(t *testing.T) {
	s, _ := reviewSession(t)
	waiting := make(chan struct{})
	giveUp := make(chan struct{}) // closed on failure, so nothing is left waiting
	places := func(ctx context.Context) (*location.Resolver, error) {
		close(waiting)
		select {
		case <-ctx.Done():
		case <-giveUp:
		}
		return nil, ctx.Err()
	}
	built := make(chan error, 1)
	go func() {
		_, err := s.Review(context.Background(), places)
		built <- err
	}()
	<-waiting
	held := make(chan bool, 1)
	go func() {
		release, ok := s.Work.HoldLibrary()
		if ok {
			release()
		}
		held <- ok
	}()
	select {
	case ok := <-held:
		if !ok {
			t.Error("the library could not be held while a review waited for places")
		}
	case <-time.After(5 * time.Second):
		close(giveUp)
		t.Fatal("a review waiting for places kept the library held")
	}
	shut := make(chan struct{})
	go func() { s.Shutdown(); close(shut) }()
	select {
	case <-shut:
	case <-time.After(5 * time.Second):
		close(giveUp)
		t.Fatal("shutdown waited for the location database")
	}
	select {
	case <-built:
	case <-time.After(5 * time.Second):
		close(giveUp)
		t.Fatal("the review kept waiting for places after shutdown")
	}
}

// A peek reads its folder's files from the review's own library: once the
// plan moved on (a copy, another library) the folder ids name nothing there.
func TestPeekRefusedOnceThePlanMovedOn(t *testing.T) {
	ctx := context.Background()
	s, day := reviewSession(t)
	r, err := s.Review(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.Work.StartJob(JobCopy)
	if err != nil {
		t.Fatal(err)
	}
	done()
	if _, err := r.Peek(ctx, vfs.Node{ID: day, Name: "03"}); !errors.Is(err, ErrStaleReview) {
		t.Errorf("peek after the plan moved on = %v, want ErrStaleReview", err)
	}
}

// Work that reads or edits a plan opens an existing library itself, as Scan
// does, but never creates one where none is.
func TestReadsOpenAnExistingLibraryOnly(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if _, err := s.CopyPlan(ctx); !errors.Is(err, ErrNotOpen) {
		t.Errorf("copy plan with no library = %v, want ErrNotOpen", err)
	}
	if s.LibraryExists() {
		t.Fatal("reading a copy plan created a library")
	}

	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, s.DB(), 1, "/src", "a.jpg", 1)
	dbtest.SeedEntry(t, s.DB(), 1, "/src/a.jpg", "2024/06_June/03/a.jpg")
	s.Close()
	p, err := s.CopyPlan(ctx)
	if err != nil || p.Files != 1 {
		t.Errorf("copy plan of a closed library = %+v, %v; want it opened and 1 file", p, err)
	}
	s.Close()
	if _, err := s.Review(ctx, nil); err != nil {
		t.Errorf("review of a closed library: %v", err)
	}
}

// A restore closes the library this session has open, puts the backup back,
// and opens it again: a front end needn't know the order.
func TestRestoreReopensTheLibrary(t *testing.T) {
	ctx := context.Background()
	s, _ := reviewSession(t)
	if err := s.Reset(ctx); err != nil { // backs up the planned file first
		t.Fatal(err)
	}
	if err := s.Restore(ctx); err != nil {
		t.Fatalf("restore with the library open: %v", err)
	}
	if s.DB() == nil {
		t.Fatal("the library was left closed after the restore")
	}
	if p, err := s.CopyPlan(ctx); err != nil || p.Files != 1 {
		t.Errorf("after restore, copy plan = %+v, %v; want the backup's 1 file", p, err)
	}
}
