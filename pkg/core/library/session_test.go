package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/core/verify"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
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
	s := New(cfg, logger.NewNoopLogger(), testLogFile(t, t.TempDir()))
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
	cfg.SetOutput(s.OutputDir())
	next := New(cfg, logger.NewNoopLogger(), nil)
	if err := next.Open(ctx); err != nil {
		t.Fatalf("Open (second session): %v", err)
	}
	defer next.Close()
	if !next.Settings().Equal(want) {
		t.Errorf("settings = %+v, want the library's own %+v", next.Settings(), want)
	}
	if hist := next.Configuration().History(); len(hist) == 0 || hist[0] != cfg.OutputDir() {
		t.Errorf("history = %v, want the library just opened at the front", hist)
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
	s := New(&config.Configuration{AppDBPath: filepath.Join(dir, ".wandersort.db")}, logger.NewNoopLogger(), testLogFile(t, t.TempDir()))
	if err := s.Open(context.Background()); err == nil {
		t.Fatal("Open must refuse a folder holding someone else's files")
	}
	if s.LogFile().Path() == "" {
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

// noPlaces stands in for the location database: these libraries name no places.
func noPlaces(context.Context) (*location.Resolver, error) { return nil, nil }

// A setting reaches the library together with the plan built from it; a save
// that fails leaves the library's settings and the session's as they were.
func TestSaveSettings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		saved bool
	}{
		{"saved with the plan", true},
		{"a write that fails keeps the old settings", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := testSession(t)
			if err := s.Open(ctx); err != nil {
				t.Fatal(err)
			}
			if !tc.saved {
				// aborts the save's transaction from inside, where the plan is written
				if _, err := s.DB().SQL.ExecContext(ctx, `CREATE TRIGGER fail_save BEFORE INSERT ON library_settings
					BEGIN SELECT RAISE(ABORT, 'disk gone'); END`); err != nil {
					t.Fatal(err)
				}
			}
			old := s.Settings()
			changed := old
			changed.Rules = []string{"device"}
			// saved off the goroutine that keeps reading, as a UI would
			result := make(chan error, 1)
			go func() { result <- s.SaveSettings(ctx, changed, noPlaces) }()
			var err error
		reading:
			for {
				select {
				case err = <-result:
					break reading
				default:
					_, _ = s.Settings(), s.DB()
				}
			}
			if (err == nil) != tc.saved {
				t.Fatalf("SaveSettings = %v, want saved %v", err, tc.saved)
			}
			want := old
			if tc.saved {
				want = changed
			}
			onDisk, err := config.LoadSettings(ctx, s.DB())
			if err != nil {
				t.Fatal(err)
			}
			if !onDisk.Equal(want) || !s.Settings().Equal(want) {
				t.Errorf("library %v, session %v; want %v in both", onDisk.Rules, s.Settings().Rules, want.Rules)
			}
		})
	}
}

// A new folder starts an empty library with the same settings; an organised one opens with its own.
func TestSwitch(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	first := s.OutputDir()
	settings := s.Settings()
	settings.Rules = []string{"date"}
	if err := s.SaveSettings(ctx, settings, noPlaces); err != nil {
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
	if s.OutputDir() != second || files != 0 || !slices.Equal(s.Settings().Rules, []string{"date"}) {
		t.Errorf("new library at %q: files=%d rules=%v, want empty with the old rules", s.OutputDir(), files, s.Settings().Rules)
	}

	settings = s.Settings()
	settings.Rules = []string{"location"}
	if err := s.SaveSettings(ctx, settings, noPlaces); err != nil {
		t.Fatal(err)
	}
	if err := s.Switch(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().SQL.GetContext(ctx, &files, `SELECT COUNT(*) FROM file_registry`); err != nil {
		t.Fatal(err)
	}
	if files != 1 || !slices.Equal(s.Settings().Rules, []string{"date"}) {
		t.Errorf("back in the first library: files=%d rules=%v, want its own file and rules", files, s.Settings().Rules)
	}
}

// "New" is decided by what the library holds, not by its file being there: a
// library left half made (created, settings never written) still takes the
// current settings, and one already holding files keeps the defaults it runs on.
func TestSwitchCarriesSettingsOnlyIntoAnUnusedLibrary(t *testing.T) {
	ctx := context.Background()
	carried := []string{"device"}
	for _, tc := range []struct {
		name string
		make func(t *testing.T, dbPath string)
		want []string
	}{
		{"half made", func(t *testing.T, dbPath string) {
			d, err := db.New(ctx, dbPath, logger.NewNoopLogger())
			if err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
		}, carried},
		{"holding files", seedPlannedFile, config.DefaultSettings().Rules},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testSession(t)
			if err := s.Open(ctx); err != nil {
				t.Fatal(err)
			}
			settings := s.Settings()
			settings.Rules = carried
			if err := s.SaveSettings(ctx, settings, noPlaces); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "library")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			tc.make(t, filepath.Join(dir, filepath.Base(config.DBPath(s.OutputDir()))))
			if err := s.Switch(ctx, dir); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(s.Settings().Rules, tc.want) {
				t.Errorf("rules = %v, want %v", s.Settings().Rules, tc.want)
			}
		})
	}
}

// A switch that fails leaves the session as it was: nothing open stays nothing
// open, and an open library is never closed.
func TestSwitchFailureRestores(t *testing.T) {
	ctx := context.Background()
	busy := filepath.Join(t.TempDir(), "busy")
	held, err := lock.AcquireOutput(busy)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Unlock()

	s := testSession(t)
	old := s.OutputDir()
	if err := s.Switch(ctx, busy); err == nil {
		t.Fatal("switched to a library another process holds")
	}
	if s.DB() != nil || s.LibraryExists() || s.OutputDir() != old {
		t.Errorf("with nothing open: open=%v exists=%v dir=%q, want nothing opened or created at %q",
			s.DB() != nil, s.LibraryExists(), s.OutputDir(), old)
	}

	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	before := s.DB()
	if err := s.Switch(ctx, busy); err == nil {
		t.Fatal("switched to a library another process holds")
	}
	if s.DB() != before || s.OutputDir() != old {
		t.Errorf("with a library open: same handle=%v dir=%q, want %q left open", s.DB() == before, s.OutputDir(), old)
	}
}

// The open library named another way (here a symlink) is the same library:
// opening it beside itself would find its own lock taken.
func TestSwitchToTheOpenLibrarySpelledOtherwise(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	old, before := s.OutputDir(), s.DB()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(old, alias); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := s.Switch(ctx, alias); err != nil {
		t.Fatalf("Switch to the open library through a symlink: %v", err)
	}
	if s.DB() != before || s.OutputDir() != old {
		t.Errorf("same handle=%v dir=%q, want the open library kept at %q", s.DB() == before, s.OutputDir(), old)
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
			if _, err := os.Stat(config.DBPath(s.OutputDir())); !os.IsNotExist(err) {
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
			fakeLibrary(t, config.DBPath(s.OutputDir()))
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
			seedPlannedFile(t, config.DBPath(s.OutputDir()))
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
		// An unreadable draft still means something is waiting: execute is
		// where that surfaces, and the user needs telling either way.
		{"AnUnreadableDraftStillCounts", func(t *testing.T) {
			s := testSession(t)
			fakeLibrary(t, config.DBPath(s.OutputDir()))
			// A bad line that is not the last one: a torn final line is a
			// crash mid-append and heals itself, this is corruption.
			draft := filepath.Join(s.OutputDir(), vfs.DraftFileName)
			if err := os.WriteFile(draft, []byte("{not json\n{\"seq\":2,\"op\":\"drop\"}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := vfs.OpenDraft(s.OutputDir(), nil); err == nil {
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

// A plan rewrite waits for work already on the library, keeps other work off
// it while it runs, and two of them run one after the other. Rewriting stays
// true until the last one ends.
func TestWorkGroupRewrite(t *testing.T) {
	var g WorkGroup
	reading, _ := g.StartOnLibrary()
	enterFirst, err := g.StartRewrite()
	enterSecond, err2 := g.StartRewrite()
	if err != nil || err2 != nil || !g.Rewriting() {
		t.Fatalf("rewrites registered: %v %v, rewriting=%v", err, err2, g.Rewriting())
	}

	firstIn := make(chan func())
	go func() { done, _ := enterFirst(); firstIn <- done }()
	select {
	case <-firstIn:
		t.Fatal("a rewrite started beside work running on the library")
	case <-time.After(50 * time.Millisecond):
	}
	reading()
	endFirst := <-firstIn

	secondIn := make(chan func())
	go func() { done, _ := enterSecond(); secondIn <- done }()
	// a read is refused, not queued: a re-plan may run for minutes
	read := make(chan error, 1)
	go func() {
		done, err := g.StartOnLibrary()
		if done != nil {
			done()
		}
		read <- err
	}()
	select {
	case err := <-read:
		if !errors.Is(err, ErrReplanning) {
			t.Errorf("read during a rewrite = %v, want ErrReplanning", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a read waited for a rewrite instead of being refused")
	}
	select {
	case <-secondIn:
		t.Fatal("two rewrites ran at once")
	case <-time.After(50 * time.Millisecond):
	}
	endFirst()
	if !g.Rewriting() {
		t.Error("Rewriting cleared while a second rewrite was still to run")
	}
	(<-secondIn)()
	if g.Rewriting() {
		t.Error("Rewriting still set after the last rewrite ended")
	}
}

// A rewrite registered but never entered (the program ended before its work
// ran) does not hold up shutdown, and entering it afterwards is refused.
func TestWorkGroupShutdownSkipsARewriteNeverEntered(t *testing.T) {
	var g WorkGroup
	enter, _ := g.StartRewrite()
	closed := make(chan struct{})
	go func() {
		g.CloseAndWait()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for a rewrite that never entered")
	}
	if _, err := enter(); !errors.Is(err, ErrClosing) || g.Rewriting() {
		t.Errorf("a rewrite entered after shutdown, or still counts: rewriting=%v", g.Rewriting())
	}
}

// A library opened before that is no longer in its folder (its drive
// unplugged) is refused, not started afresh there or replaced by the default
// one: whether the folder went with the drive or an empty mount point stayed.
func TestOpenRefusesAMissingRememberedLibrary(t *testing.T) {
	tests := []struct {
		name       string
		mountPoint bool // an empty folder stays where the drive was mounted
	}{
		{"folder gone", false},
		{"empty mount point", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := testSession(t)
			gone := filepath.Join(t.TempDir(), "on-a-drive")
			if err := s.Switch(ctx, gone); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if err := os.RemoveAll(gone); err != nil {
				t.Fatal(err)
			}
			if tt.mountPoint {
				if err := os.Mkdir(gone, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			noLibraryStarted := func(step string) {
				t.Helper()
				if _, err := os.Stat(config.DBPath(gone)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s started a new library in the missing one's folder: %v", step, err)
				}
				if _, err := os.Stat(gone); tt.mountPoint == errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s changed whether the folder exists: %v", step, err)
				}
			}

			cfg, err := config.New()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.OutputDir() != gone {
				t.Fatalf("library chosen = %q, want the most recent %q", cfg.OutputDir(), gone)
			}
			next := New(cfg, logger.NewNoopLogger(), nil)
			t.Cleanup(next.Close)
			if err := next.Open(ctx); !errors.Is(err, config.ErrLibraryMissing) {
				t.Fatalf("Open = %v, want ErrLibraryMissing", err)
			}
			noLibraryStarted("Open")
			if err := next.Restore(ctx); !errors.Is(err, config.ErrLibraryMissing) {
				t.Errorf("Restore = %v, want ErrLibraryMissing", err)
			}
			noLibraryStarted("Restore")
			if err := s.Switch(ctx, filepath.Join(t.TempDir(), "other")); err != nil {
				t.Fatal(err)
			}
			if err := s.Switch(ctx, gone); !errors.Is(err, config.ErrLibraryMissing) {
				t.Errorf("Switch = %v, want ErrLibraryMissing", err)
			}
			noLibraryStarted("Switch")
		})
	}
}

// A plan rewrite reads the settings and the library while it waits or runs,
// so neither changes under it.
func TestRewriteRefusesSettingsAndSwitch(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	settings := s.Settings()
	settings.Rules = []string{"date"}
	enter, _ := s.Work.StartRewrite()
	if err := s.SaveSettings(ctx, settings, noPlaces); !errors.Is(err, ErrReplanning) {
		t.Errorf("SaveSettings mid rewrite = %v, want ErrReplanning", err)
	}
	if err := s.Switch(ctx, filepath.Join(t.TempDir(), "other")); !errors.Is(err, ErrReplanning) {
		t.Errorf("Switch mid rewrite = %v, want ErrReplanning", err)
	}
	done, _ := enter()
	done()
	if err := s.SaveSettings(ctx, settings, noPlaces); err != nil {
		t.Errorf("SaveSettings once the rewrite ended: %v", err)
	}
}

// A save while a switch holds the library is refused, not queued behind it:
// it would land in the library switched to, with settings chosen for another.
func TestSaveSettingsRefusedWhileTheLibraryIsHeld(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	stored := s.Settings()

	release, err := s.Work.HoldLibrary()
	if err != nil {
		t.Fatalf("library not held: %v", err)
	}
	settings := s.Settings()
	settings.Rules = []string{"date"}
	saved := make(chan error, 1)
	go func() { saved <- s.SaveSettings(ctx, settings, noPlaces) }()
	select {
	case err = <-saved:
		release()
	case <-time.After(5 * time.Second):
		release()
		<-saved
		t.Fatal("a save queued behind the hold instead of being refused")
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("save while a switch holds the library = %v, want ErrBusy", err)
	}
	got, err := config.LoadSettings(ctx, s.DB())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(stored) || !s.Settings().Equal(stored) {
		t.Errorf("library holds rules %v (session %v), want its own %v", got.Rules, s.Settings().Rules, stored.Rules)
	}
}

// Jobs run one at a time and never beside a re-plan: a scan replacing the
// pending plan under a copy working through it would copy files the plan no
// longer holds. Each one started moves Plan on.
func TestWorkGroupJobs(t *testing.T) {
	var g WorkGroup
	plan := g.Plan()
	done, err := g.StartJob(JobScan)
	if err != nil {
		t.Fatal(err)
	}
	if g.Plan() == plan || g.Running() != JobScan {
		t.Errorf("after a scan started: plan %d (was %d), running %q", g.Plan(), plan, g.Running())
	}
	if wrong, err := g.StartJob(JobCopy); !errors.Is(err, ErrBusy) {
		if wrong != nil {
			wrong() // or the re-plan below waits on it for ever
		}
		t.Fatalf("copy beside a scan = %v, want ErrBusy", err)
	} else if busy, ok := errors.AsType[*BusyError](err); !ok || busy.Job != JobScan {
		t.Errorf("copy beside a scan = %#v, want a BusyError naming the scan", err)
	}
	if _, err := g.StartRewrite(); !errors.Is(err, ErrBusy) {
		t.Errorf("re-plan beside a scan = %v, want ErrBusy", err)
	}
	if _, err := g.HoldLibrary(); err == nil {
		t.Error("library held for a switch while a scan ran")
	} else if busy, ok := errors.AsType[*BusyError](err); !ok || busy.Job != JobScan {
		t.Errorf("hold beside a scan = %v, want a BusyError naming the scan", err)
	}
	done()

	enter, err := g.StartRewrite()
	if err != nil {
		t.Fatal(err)
	}
	if wrong, err := g.StartJob(JobCopy); !errors.Is(err, ErrReplanning) {
		if wrong != nil {
			wrong() // or entering the re-plan waits on it for ever
		}
		t.Fatalf("copy while a re-plan waits = %v, want ErrReplanning", err)
	}
	end, _ := enter()
	end()
	if done, err := g.StartJob(JobCopy); err != nil {
		t.Errorf("copy once the re-plan ended: %v", err)
	} else {
		done()
	}

	g.CloseAndWait()
	if _, err := g.StartJob(JobScan); !errors.Is(err, ErrClosing) {
		t.Errorf("scan after shutdown = %v, want ErrClosing", err)
	}
}

// A switch is refused while a job runs, and once shutdown began: nothing would
// release the lock it took.
func TestSwitchRefusedWhileBusyOrClosing(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	done, err := s.Work.StartJob(JobCopy)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Switch(ctx, filepath.Join(t.TempDir(), "other")); !errors.Is(err, ErrBusy) {
		t.Errorf("Switch beside a copy = %v, want ErrBusy", err)
	}
	done()
	s.Work.CloseAndWait()
	other := filepath.Join(t.TempDir(), "after")
	if err := s.Switch(ctx, other); !errors.Is(err, ErrClosing) {
		t.Errorf("Switch after shutdown = %v, want ErrClosing", err)
	}
	if _, err := os.Stat(other); !errors.Is(err, os.ErrNotExist) {
		t.Error("a switch after shutdown made a library")
	}
}

// Shutdown waits for running work and refuses work that starts after it.
func TestWorkGroupShutdown(t *testing.T) {
	var g WorkGroup
	done, ok := g.Start()
	if !ok {
		t.Fatal("Start refused before shutdown")
	}
	finished := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(finished)
		done()
	}()
	g.CloseAndWait()
	select {
	case <-finished:
	default:
		t.Fatal("CloseAndWait returned before the running work finished")
	}
	if _, ok := g.Start(); ok {
		t.Error("work started after shutdown")
	}
}

// The library can't be switched while work runs on it, and work waits out a switch.
func TestWorkGroupHoldLibrary(t *testing.T) {
	var g WorkGroup
	// a short read is waited out, not refused: a front end polling a copy
	// plan must not make every switch look busy
	reading, err := g.StartOnLibrary()
	if err != nil {
		t.Fatalf("StartOnLibrary refused: %v", err)
	}
	held := make(chan func())
	go func() {
		release, err := g.HoldLibrary()
		if err != nil {
			t.Errorf("hold refused beside a short read: %v", err)
		}
		held <- release
	}()
	select {
	case <-held:
		t.Fatal("library held while a read ran on it")
	case <-time.After(50 * time.Millisecond):
	}
	// no job starts while a hold waits: it would keep the hold waiting for good
	refused := make(chan error, 1)
	go func() {
		done, err := g.StartJob(JobScan)
		if done != nil {
			done()
		}
		refused <- err
	}()
	select {
	case err := <-refused:
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("job beside a waiting hold = %v, want ErrBusy", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a job beside a waiting hold started and waited behind it")
	}
	reading()
	release := <-held

	started := make(chan struct{})
	go func() {
		done, _ := g.StartOnLibrary()
		close(started)
		done()
	}()
	select {
	case <-started:
		t.Fatal("work started on a library being switched")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	<-started

	// a job is refused, not waited for: it may run for hours
	done, err := g.StartJob(JobCopy)
	if err != nil {
		t.Fatal(err)
	}
	if release, err := g.HoldLibrary(); err == nil {
		release()
		t.Error("library held while a copy ran on it")
	}
	done()
}

// Any goroutine may use the session while another switches libraries and
// saves settings: readers never see the library half swapped (none open, or a
// closed handle), and -race sees every field read under the session's lock.
func TestSessionConcurrentUse(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	dirs := []string{s.OutputDir(), filepath.Join(t.TempDir(), "other")}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	read := func(check func() bool) {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if !check() {
					return
				}
			}
		}()
	}
	for range 2 {
		read(func() bool {
			if !s.State(ctx).Open || s.DB() == nil {
				t.Error("a reader saw no library open mid switch")
				return false
			}
			return true
		})
		// getters alone: a lock taken just before would hide an unguarded read from -race
		read(func() bool { _ = s.Settings(); return true })
		read(func() bool { _ = s.OutputDir(); return true })
		read(func() bool { _ = s.Configuration(); return true })
	}

	for i := range 20 {
		if err := s.Switch(ctx, dirs[(i+1)%2]); err != nil {
			t.Fatal(err)
		}
		settings := s.Settings()
		settings.CollapseLevels = i%2 == 0
		if err := s.SaveSettings(ctx, settings, noPlaces); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
}

// Close waits for work running on the library before shutting its database.
func TestCloseWaitsForWorkOnTheLibrary(t *testing.T) {
	s := testSession(t)
	if err := s.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	done, _ := s.Work.StartOnLibrary()
	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close shut the library under running work")
	case <-time.After(50 * time.Millisecond):
	}
	if s.DB() == nil {
		t.Fatal("the database closed under running work")
	}
	done()
	<-closed
	if s.DB() != nil {
		t.Error("Close left the library open")
	}
}

// Shutdown stops the work running on the library before it waits for it, so
// a caller needs no cancel of its own to quit mid-copy.
func TestShutdownStopsRunningWork(t *testing.T) {
	s := testSession(t)
	done, err := s.Work.StartJob(JobCopy)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := s.bound(context.Background())
	defer cancel()
	go func() { // the copy: runs until its context ends
		<-ctx.Done()
		done()
	}()
	shut := make(chan struct{})
	go func() { s.Shutdown(); close(shut) }()
	select {
	case <-shut:
	case <-time.After(5 * time.Second):
		cancel() // let the stuck Shutdown finish
		t.Fatal("Shutdown waited for work it never stopped")
	}
	if _, err := s.Work.StartJob(JobScan); !errors.Is(err, ErrClosing) {
		t.Errorf("job after Shutdown = %v, want ErrClosing", err)
	}
	// nothing would close a library opened now
	if err := s.Open(context.Background()); !errors.Is(err, ErrClosing) {
		t.Errorf("Open after Shutdown = %v, want ErrClosing", err)
	}
	if s.DB() != nil {
		t.Error("a library was opened after Shutdown")
	}
}

// What a front end may start depends on the work running: nothing beside a
// job or a re-plan, and no review edit while a copy applies the draft.
func TestStateRules(t *testing.T) {
	planned := State{Open: true, Planned: 3}
	tests := []struct {
		name                              string
		running                           Job
		replanning                        bool
		organise, copy, scan, setSettings bool
	}{
		{"idle", "", false, true, true, true, true},
		{"copy running", JobCopy, false, false, false, false, false},
		{"scan running", JobScan, false, false, false, false, false},
		{"re-plan", "", true, false, false, false, false},
	}
	for _, tt := range tests {
		st := planned
		st.Running, st.Replanning = tt.running, tt.replanning
		got := [4]bool{st.CanOrganise(), st.CanCopy(), st.CanScan(), st.CanChangeSettings()}
		if want := [4]bool{tt.organise, tt.copy, tt.scan, tt.setSettings}; got != want {
			t.Errorf("%s: organise, copy, scan, settings = %v, want %v", tt.name, got, want)
		}
	}
	if (State{Open: true}).CanCopy() {
		t.Error("CanCopy with nothing planned")
	}
}

// Check forgets records and Reset wipes them, so neither runs beside a copy.
func TestCheckAndResetWaitForNoWork(t *testing.T) {
	ctx := context.Background()
	s := testSession(t)
	if err := s.Open(ctx); err != nil {
		t.Fatal(err)
	}
	done, err := s.Work.StartJob(JobCopy)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if _, err := s.Check(ctx, verify.Options{}); !errors.Is(err, ErrBusy) {
		t.Errorf("check beside a copy = %v, want ErrBusy", err)
	}
	if err := s.Reset(ctx); !errors.Is(err, ErrBusy) {
		t.Errorf("reset beside a copy = %v, want ErrBusy", err)
	}
}
