package library

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// testSession is a session over a fresh default library, with $HOME moved to a temp folder.
func testSession(t *testing.T) *Session {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg, err := config.New()
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Config: cfg, Log: logger.NewNoopLogger(), LogFile: testLogFile(t, t.TempDir())}
	t.Cleanup(s.Close) // Windows can't remove an open database
	return s
}

// testLogFile is a log file in dir, closed before the test's temp folders are
// removed: Windows can't remove an open file.
func testLogFile(t *testing.T, dir string) *logger.File {
	t.Helper()
	f := logger.NewFile(dir)
	t.Cleanup(func() { f.Close() })
	return f
}

// A library's rules come back with it, and opening it makes it the most recently used one.
func TestOpenLoadsThatLibrarysSettings(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := config.Settings{Rules: []string{"device"}, CollapseLevels: true}
	if err := config.SaveSettings(ctx, s.DB(), want); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// A second session over the same folder, without moving $HOME (which would
	// lose the history the first wrote): only the library says what its rules are.
	cfg, err := config.New()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetOutput(s.Config.OutputDir())
	next := &Session{Config: cfg, Log: logger.NewNoopLogger()}
	if err := next.Open(ctx); err != nil {
		t.Fatalf("Open (second session): %v", err)
	}
	defer next.Close()
	if !next.Config.Settings.Equal(want) {
		t.Errorf("settings = %+v, want the library's own %+v", next.Config.Settings, want)
	}
	if hist := next.Config.History(); len(hist) == 0 || hist[0] != cfg.OutputDir() {
		t.Errorf("history = %v, want the library just opened at the front", hist)
	}
}

// Opening twice must not replace the open handle, which would leak the first one.
func TestOpenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	first := s.DB()
	if first == nil {
		t.Fatal("Open left no database")
	}
	if err := s.Open(ctx); err != nil {
		t.Fatalf("Open (second call): %v", err)
	}
	if s.DB() != first {
		t.Error("Open replaced an already-open database handle")
	}
}

// The check runs before the lock, so a folder that is not a library gets no
// lock file and no database. The attempt itself is worth a log, so the run's
// log is persisted all the same.
func TestOpenRefusedWritesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "IMG_0001.JPG"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Session{
		Config:  &config.Configuration{AppDBPath: filepath.Join(dir, ".wandersort.db")},
		Log:     logger.NewNoopLogger(),
		LogFile: testLogFile(t, t.TempDir()),
	}
	if err := s.Open(context.Background()); err == nil {
		t.Fatal("Open must refuse a folder holding someone else's files")
	}
	if s.LogFile.Path() == "" {
		t.Error("Open must persist the run's log")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("refused folder holds %d entries, want only the user's file", len(entries))
	}
}

// Close runs in every deferred cleanup, including paths that never opened anything.
func TestCloseWithNothingOpen(t *testing.T) {
	s := &Session{Log: logger.NewNoopLogger()}
	s.Close() // must not panic
}

// A new folder starts an empty library with the same settings; an organised one opens with its own.
func TestSwitch(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	first := s.Config.OutputDir()
	settings := s.Config.Settings
	settings.Rules = []string{"date"}
	if err := s.SaveSettings(ctx, first, settings); err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, s.DB(), 1, "/src", "a.jpg", 1)

	second := filepath.Join(t.TempDir(), "second")
	if err := s.Switch(ctx, second); err != nil {
		t.Fatal(err)
	}
	var files int
	if err := s.DB().SQL.GetContext(ctx, &files, `SELECT COUNT(*) FROM file_registry`); err != nil {
		t.Fatal(err)
	}
	if s.Config.OutputDir() != second || files != 0 || !slices.Equal(s.Config.Rules, []string{"date"}) {
		t.Errorf("new library at %q: files=%d rules=%v, want empty with the old rules", s.Config.OutputDir(), files, s.Config.Rules)
	}

	settings = s.Config.Settings
	settings.Rules = []string{"location"}
	if err := s.SaveSettings(ctx, second, settings); err != nil {
		t.Fatal(err)
	}
	if err := s.Switch(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().SQL.GetContext(ctx, &files, `SELECT COUNT(*) FROM file_registry`); err != nil {
		t.Fatal(err)
	}
	if files != 1 || !slices.Equal(s.Config.Rules, []string{"date"}) {
		t.Errorf("back in the first library: files=%d rules=%v, want its own file and rules", files, s.Config.Rules)
	}
}

func TestState(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// Reading the state must never be the thing that creates a library:
		// a session that only looks around writes nothing outside the logs.
		{"ReadsNothingIntoExistence", func(t *testing.T) {
			s := testSession(t)
			st := s.State(context.Background())
			if st.Exists || st.Open {
				t.Errorf("state = %+v, want an empty folder", st)
			}
			if _, err := os.Stat(s.Config.AppDBPath); !os.IsNotExist(err) {
				t.Error("reading the state created a database")
			}
			if st.CanReview() {
				t.Error("CanReview with no library at all")
			}
		}},
		// Before the library is open, the file being there is the whole
		// answer: finding out for real means opening it, which is what the
		// caller is deciding whether to do.
		{"AClosedLibraryIsAnsweredByItsFile", func(t *testing.T) {
			s := testSession(t)
			fakeLibrary(t, s.Config.AppDBPath)
			st := s.State(context.Background())
			if !st.Exists || st.Open || st.Planned != 0 {
				t.Errorf("state = %+v, want exists and shut", st)
			}
			if !st.CanReview() {
				t.Error("a library on disk should be reviewable before it is opened")
			}
		}},
		// Once it is open the count is the answer, so a library with nothing
		// left to plan stops claiming to have something to review.
		{"AnOpenLibraryIsAnsweredByItsCount", func(t *testing.T) {
			s := testSession(t)
			seedPlannedFile(t, s.Config.AppDBPath)
			if err := s.Open(context.Background()); err != nil {
				t.Fatal(err)
			}
			st := s.State(context.Background())
			if !st.Open || st.Planned != 1 {
				t.Fatalf("state = %+v, want one planned file", st)
			}
			if !st.CanReview() {
				t.Error("a planned file should be reviewable")
			}
			if _, err := s.DB().SQL.Exec(`UPDATE file_registry SET placed = 1`); err != nil {
				t.Fatal(err)
			}
			if st := s.State(context.Background()); st.Planned != 0 || st.CanReview() {
				t.Errorf("state = %+v, want nothing left to review once everything is placed", st)
			}
		}},
		{"CountsTheReviewEditsWaiting", func(t *testing.T) {
			s := testSession(t)
			fakeLibrary(t, s.Config.AppDBPath)
			writeDraft(t, s.Config.OutputDir(),
				vfs.Edit{Seq: 1, Op: vfs.OpRename, Node: 1, From: "03", To: "<place>"},
				vfs.Edit{Seq: 2, Op: vfs.OpDrop, Nodes: []int64{2}})
			if st := s.State(context.Background()); st.Edits != 2 || !st.HasEdits() {
				t.Errorf("state = %+v, want two edits waiting", st)
			}
		}},
		// An unreadable draft still means something is waiting: execute is
		// where that surfaces, and the user needs telling either way.
		{"AnUnreadableDraftStillCounts", func(t *testing.T) {
			s := testSession(t)
			fakeLibrary(t, s.Config.AppDBPath)
			// A bad line that is not the last one: a torn final line is a
			// crash mid-append and heals itself, this is corruption.
			draft := filepath.Join(s.Config.OutputDir(), vfs.DraftFileName)
			if err := os.WriteFile(draft, []byte("{not json\n{\"seq\":2,\"op\":\"drop\"}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := vfs.OpenDraft(s.Config.OutputDir(), nil); err == nil {
				t.Fatal("this draft should be unreadable, or the test proves nothing")
			}
			if st := s.State(context.Background()); !st.HasEdits() {
				t.Errorf("state = %+v, want the draft counted", st)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// fakeLibrary leaves an empty database file at dbPath: enough for anything that only stats it.
func fakeLibrary(t *testing.T, dbPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedPlannedFile leaves a real library at dbPath holding one file that is planned and not yet placed.
func seedPlannedFile(t *testing.T, dbPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := db.New(context.Background(), dbPath, logger.NewNoopLogger())
	if err != nil {
		t.Fatal(err)
	}
	dbtest.SeedFile(t, d, 1, "/src", "A.jpg", 5)
	dbtest.SeedEntry(t, d, 1, "/src/A.jpg", "2024/A.jpg")
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeDraft writes edits as the review's journal in dir, without a plan they apply to.
func writeDraft(t *testing.T, dir string, edits ...vfs.Edit) {
	t.Helper()
	var buf []byte
	for _, e := range edits {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		buf = append(append(buf, line...), '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, vfs.DraftFileName), buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Shutdown waits for running work and refuses work that starts after it.
func TestWorkGroupShutdown(t *testing.T) {
	var g WorkGroup
	if !g.Start() {
		t.Fatal("Start refused before shutdown")
	}
	finished := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(finished)
		g.Done()
	}()
	g.CloseAndWait()
	select {
	case <-finished:
	default:
		t.Fatal("CloseAndWait returned before the running work finished")
	}
	if g.Start() {
		t.Error("work started after shutdown")
	}
}

// The library can't be switched while work runs on it, and work waits out a switch.
func TestWorkGroupHoldLibrary(t *testing.T) {
	var g WorkGroup
	if !g.StartOnLibrary() {
		t.Fatal("StartOnLibrary refused")
	}
	if _, ok := g.HoldLibrary(); ok {
		t.Fatal("library held while work ran on it")
	}
	g.DoneOnLibrary()
	release, ok := g.HoldLibrary()
	if !ok {
		t.Fatal("library not held once the work finished")
	}
	started := make(chan struct{})
	go func() {
		g.StartOnLibrary()
		close(started)
		g.DoneOnLibrary()
	}()
	select {
	case <-started:
		t.Fatal("work started on a library being switched")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	<-started
}
