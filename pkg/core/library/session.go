package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Session is one process's hold on at most one library: its database, the
// output lock that keeps other processes out, and the work running on it.
// Every method may be called from any goroutine.
type Session struct {
	// Work is background work on the library; Close waits for work on it.
	Work WorkGroup

	log     logger.Logger
	logFile *logger.File

	// stopping ends with Shutdown; work the session runs stops with it
	stopping context.Context
	stop     context.CancelFunc

	// mu guards the fields below. Taken after Work's library lock, never
	// before it.
	mu   sync.RWMutex
	cfg  config.Configuration
	db   *db.DB
	lock *lock.Lock
}

// New is a session over cfg's output folder with no library open yet. It
// keeps its own copy of cfg. logFile is this process's log, buffered in memory
// until a library opens (or a warning) persists it; nil means no file log.
func New(cfg *config.Configuration, log logger.Logger, logFile *logger.File) *Session {
	stopping, stop := context.WithCancel(context.Background())
	return &Session{cfg: copyConfig(*cfg), log: log, logFile: logFile, stopping: stopping, stop: stop}
}

// Shutdown stops the work running on the library, waits for it, then closes
// the library. Nothing new starts once it has begun (ErrClosing).
func (s *Session) Shutdown() {
	s.stop()
	s.Work.CloseAndWait()
	s.Close()
}

// bound is ctx, also cancelled once Shutdown begins.
func (s *Session) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	unhook := context.AfterFunc(s.stopping, cancel)
	return ctx, func() { unhook(); cancel() }
}

// copyConfig is c sharing nothing a caller could change underneath it.
func copyConfig(c config.Configuration) config.Configuration {
	c.Settings = copySettings(c.Settings)
	return c
}

func copySettings(s config.Settings) config.Settings {
	s.Rules = slices.Clone(s.Rules)
	return s
}

// Log is the session's logger.
func (s *Session) Log() logger.Logger { return s.log }

// LogFile is this process's log file, or nil.
func (s *Session) LogFile() *logger.File { return s.logFile }

// DB is the open library's database, or nil while none is open. Work that
// queries it runs under Work.StartOnLibrary, so no switch or close shuts it
// meanwhile.
func (s *Session) DB() *db.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db
}

// Settings is the open library's settings: what its plan was built from.
func (s *Session) Settings() config.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copySettings(s.cfg.Settings)
}

// OutputDir is the library folder the session works in.
func (s *Session) OutputDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.OutputDir()
}

// Configuration is a copy of the session's configuration as it is now.
func (s *Session) Configuration() *config.Configuration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := copyConfig(s.cfg)
	return &c
}

// LibraryExists reports whether the output folder already holds a library
// (its database file exists). Opens nothing.
func (s *Session) LibraryExists() bool {
	_, err := os.Stat(config.DBPath(s.OutputDir()))
	return err == nil
}

// Open opens the output folder as a library, once per session: check it may
// be one, take the output lock, open the database, load its settings, then
// remember it. In that order, a refused folder or a lost lock race writes
// nothing into the folder. A library another process holds fails with a
// *lock.AlreadyRunningError in the chain.
func (s *Session) Open(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return nil
	}
	// From here the run touches (or tried to touch) user data: keep its log.
	s.logFile.Persist()
	outputDir := s.cfg.OutputDir()
	if err := s.cfg.CheckRemembered(outputDir); err != nil {
		return err
	}
	if err := config.CheckLibrary(outputDir); err != nil {
		return err
	}
	appDB, l, settings, err := s.openLibrary(ctx, outputDir)
	if err != nil {
		return err
	}
	s.db, s.lock = appDB, l
	s.cfg.Settings = settings
	// only a real library goes in the recent list
	if err := s.cfg.Remember(outputDir); err != nil {
		s.log.Warn("could not record this library as recently used", "error", err)
	}
	return nil
}

// openLibrary takes the lock of the library in dir, opens its database and
// reads its settings (defaults if never saved). It publishes nothing, so a
// failure leaves nothing held.
func (s *Session) openLibrary(ctx context.Context, dir string) (*db.DB, *lock.Lock, config.Settings, error) {
	l, err := lock.AcquireOutput(dir)
	if err != nil {
		return nil, nil, config.Settings{}, fmt.Errorf("acquire lock: %w", err)
	}
	appDB, err := db.New(ctx, config.DBPath(dir), s.log)
	if err != nil {
		l.Unlock()
		return nil, nil, config.Settings{}, fmt.Errorf("app db: %w", err)
	}
	settings, err := config.LoadSettings(ctx, appDB)
	if err != nil {
		appDB.Close()
		l.Unlock()
		return nil, nil, config.Settings{}, err
	}
	return appDB, l, settings, nil
}

// Close flushes and closes the open library and releases its lock, once work
// on it has finished; with none open it does nothing.
func (s *Session) Close() {
	s.Work.library.Lock()
	defer s.Work.library.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLibrary(s.db, s.lock)
	s.db, s.lock = nil, nil
}

// closeLibrary flushes and closes d, then releases l; either may be nil.
func (s *Session) closeLibrary(d *db.DB, l *lock.Lock) {
	// a failed Close can leave WAL/SHM locked (locking_mode=EXCLUSIVE); log it
	if d != nil {
		if err := d.Writer.Flush(); err != nil {
			s.log.Error("some writes were lost before closing", "error", err)
		}
		s.log.Info("Closing databases")
		if err := d.Close(); err != nil {
			s.log.Error("failed to close app database", "error", err)
		}
	}
	l.Unlock() // nil-safe; after Close, so no other process opens the database mid-close
}

// ErrReplanning refuses changing the settings or the library while a plan
// rewrite, which reads both, is waiting or running.
var ErrReplanning = errors.New("still re-planning with the new settings; try again in a moment")

// ErrLibraryChanged refuses a settings save whose library was switched away
// while the save waited: the settings were chosen for the other one.
var ErrLibraryChanged = errors.New("the library changed before these settings were saved; nothing was saved")

// ErrClosing refuses work once the session is shutting down.
var ErrClosing = errors.New("shutting down")

// ErrNotOpen refuses work that needs the library open when none is.
var ErrNotOpen = errors.New("no library is open")

// ErrBusy refuses work that would overlap other work on the library: two
// scans, a scan and a copy, or a switch while either runs.
var ErrBusy = errors.New("busy with this library; try again when it's done")

// BusyError is ErrBusy naming the job in the way, so a front end can say which.
type BusyError struct{ Job Job }

func (e *BusyError) Error() string { return fmt.Sprintf("%s (a %s is running)", ErrBusy, e.Job) }

func (e *BusyError) Unwrap() error { return ErrBusy }

// SaveSettings makes settings the library's, opening it first if none is
// open. A change rebuilds the plan from them and writes both in one
// transaction, so the library never holds settings its plan was not built
// from: a save that fails or is cancelled leaves the library and Settings as
// they were. It waits for work running on the library, may take minutes on a
// large one, and meanwhile refuses other saves and switches (ErrReplanning).
// A scan or copy running refuses it (ErrBusy). places opens the location
// database the plan names places from; it is waited for before the library is
// held, so a download never keeps other work off the library.
func (s *Session) SaveSettings(ctx context.Context, settings config.Settings, places func(context.Context) (*location.Resolver, error)) error {
	ctx, cancel := s.bound(ctx)
	defer cancel()
	if s.Work.Rewriting() {
		return ErrReplanning
	}
	if s.DB() != nil && settings.Equal(s.Settings()) {
		return nil // nothing changed: the plan and its review edits stay
	}
	dir := s.OutputDir() // the library these settings were chosen for
	resolver, err := places(ctx)
	if err != nil {
		return fmt.Errorf("location database: %w", err)
	}
	enter, err := s.Work.StartRewrite()
	if err != nil {
		return err
	}
	done, err := enter()
	if err != nil {
		return err
	}
	defer done()
	// a switch may have taken the library while this waited
	if s.OutputDir() != dir {
		return ErrLibraryChanged
	}
	if err := s.Open(ctx); err != nil {
		return err
	}
	if settings.Equal(s.Settings()) {
		return nil
	}
	// the library can't be switched or closed while this holds it
	cfg := s.Configuration()
	cfg.Settings = copySettings(settings)
	s.log.Info("Settings changed — re-proposing the folder structure", logger.UserKey, true)
	if _, err := vfs.Propose(ctx, s.DB(), resolver, cfg, s.log); err != nil {
		return fmt.Errorf("re-plan: %w", err)
	}
	s.mu.Lock()
	s.cfg.Settings = cfg.Settings
	s.mu.Unlock()
	return nil
}

// Switch makes dir the session's library. A library there opens with its own
// settings; a new one takes the current ones. New means no settings saved and
// nothing recorded, not "no database yet", so a library a failed switch left
// half made takes them on the next try. The new library is ready before the
// old one closes, so a switch that fails leaves the session as it was.
func (s *Session) Switch(ctx context.Context, dir string) error {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs // as SetOutput: the history and the database URI need it absolute
	}
	if sameFolder(dir, s.OutputDir()) {
		return nil
	}
	if s.Work.Rewriting() {
		return ErrReplanning
	}
	if err := s.Configuration().CheckRemembered(dir); err != nil {
		return err
	}
	if err := config.CheckLibrary(dir); err != nil {
		return err
	}
	// registered, so shutdown waits for it and a switch after shutdown never
	// takes a lock nothing would release
	stop, ok := s.Work.Start()
	if !ok {
		return ErrClosing
	}
	defer stop()
	release, ok := s.Work.HoldLibrary()
	if !ok {
		return ErrBusy
	}
	defer release()
	// a save registered after the check above waits for this hold; refuse
	// rather than hand it a library it was not meant for
	if s.Work.Rewriting() {
		return ErrReplanning
	}

	s.logFile.Persist()
	appDB, l, settings, err := s.openLibrary(ctx, dir)
	if err != nil {
		return err
	}
	fresh, err := isNew(ctx, appDB)
	if err == nil && fresh {
		settings = s.Settings()
		err = config.SaveSettings(ctx, appDB, settings)
	}
	if err != nil {
		s.closeLibrary(appDB, l)
		return err
	}
	// one step: no reader ever sees the old library half closed or none open
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLibrary(s.db, s.lock)
	s.cfg.SetOutput(dir)
	s.db, s.lock = appDB, l
	s.cfg.Settings = settings
	s.Work.plan.Add(1) // another library, another plan
	if err := s.cfg.Remember(dir); err != nil {
		s.log.Warn("could not record this library as recently used", "error", err)
	}
	return nil
}

// sameFolder reports whether a and b name one folder however spelled (a
// symlink, another letter case): its lock is ours, so opening it again would
// find it busy.
func sameFolder(a, b string) bool {
	if a == b {
		return true
	}
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

// isNew reports whether the library in d has never been used: no settings
// saved and no file, folder or label recorded.
func isNew(ctx context.Context, d *db.DB) (bool, error) {
	saved, err := config.SettingsSaved(ctx, d)
	if err != nil || saved {
		return false, err
	}
	return d.IsEmpty(ctx)
}

// Restore puts the library's backup back over its database (see Restore),
// holding the output lock meanwhile. With the library open it refuses,
// db.ErrInUse: a restore needs every connection closed.
func (s *Session) Restore(ctx context.Context) error {
	release, ok := s.Work.HoldLibrary()
	if !ok {
		return ErrBusy
	}
	defer release()
	s.mu.RLock()
	open, dbPath := s.db != nil, s.cfg.AppDBPath
	s.mu.RUnlock()
	if open {
		return fmt.Errorf("restore: %w", db.ErrInUse)
	}
	// taking the output lock creates the folder: refuse first if it is a
	// remembered library whose drive is unplugged
	if err := s.Configuration().CheckRemembered(filepath.Dir(dbPath)); err != nil {
		return err
	}
	s.logFile.Persist()
	// the output lock, not the session's, keeps Open out meanwhile: an Open now
	// finds the library busy, and getters never wait for the restore
	l, err := lock.AcquireOutput(filepath.Dir(dbPath))
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer l.Unlock()
	defer s.Work.plan.Add(1) // the plan is the backup's now
	return restore(ctx, dbPath, s.log)
}

// RememberSources records the folders a scan walked; the first given ends up newest.
func (s *Session) RememberSources(roots []string) {
	s.mu.Lock() // one writer of the history files at a time
	defer s.mu.Unlock()
	for _, root := range slices.Backward(roots) {
		if err := s.cfg.RememberSource(root); err != nil {
			s.log.Warn("could not record a scanned folder as recently used", "error", err)
			return
		}
	}
}

// State answers "what can the user do next?" in one place. Exists and Edits
// are read without opening anything; Open, Planned and Placed stay zero until
// the session opens the library (which takes the lock and may create it), and
// Planned and Placed stay zero while a re-plan waits or runs.
type State struct {
	// Edits is how many review edits wait in the draft for execute. An
	// unreadable draft counts as one, so the user still hears about it.
	Edits int

	// Planned is how many files are proposed and not yet placed.
	Planned int
	// Placed is how many files are in the library.
	Placed int
	// Exists is "the output folder already holds a library". Known from a
	// stat, without opening anything.
	Exists bool

	// Open is whether the session has the library open.
	Open bool
}

// CanReview reports whether there is a plan worth reviewing: "probably" while
// the library is shut, the planned count once it is open.
func (st State) CanReview() bool {
	if st.Open {
		return st.Planned > 0
	}
	return st.Exists
}

// HasEdits reports whether review edits are waiting.
func (st State) HasEdits() bool { return st.Edits > 0 }

// State reads what the session can cheaply know about the library. Opens,
// locks and writes nothing.
func (s *Session) State(ctx context.Context) State {
	// held throughout, so the counts are of the library the folder names
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, err := os.Stat(s.cfg.AppDBPath)
	st := State{Exists: err == nil}

	// counts without repairing: a torn draft is the holder's to fix, and this
	// session may not hold the library
	if n, err := vfs.CountEdits(s.cfg.OutputDir()); err != nil {
		st.Edits = 1 // unreadable: something is there, and execute will say what
	} else {
		st.Edits = n
	}

	d := s.db
	if d == nil {
		return st
	}
	st.Open = true
	// one database connection: counting now would wait out the whole re-plan
	if s.Work.Rewriting() {
		return st
	}
	planned, _, err := execute.Pending(ctx, d)
	st.Planned = planned
	if err != nil {
		// A library we can't count is still a library; the caller keeps it
		// reachable and the screen behind it reports the real error.
		s.log.Warn("could not count the planned files", "error", err)
		st.Planned = 1
	}
	if st.Placed, err = d.PlacedCount(ctx); err != nil {
		s.log.Warn("could not count the placed files", "error", err)
	}
	return st
}

// WorkGroup tracks background work so shutdown can wait for it; work that
// would start after shutdown began is refused instead.
type WorkGroup struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	// library is read-held by work on the open library and write-held while
	// it is swapped or its plan is rewritten
	library sync.RWMutex
	// rewrites counts plan rewrites registered and not yet ended
	rewrites atomic.Int32
	// plan counts changes to the plan ever registered (see Plan)
	plan atomic.Uint64
	// job is the scan or copy running, "" for none; guarded by mu
	job Job
}

// Job is work that changes the plan or what is placed: one at a time.
type Job string

const (
	JobScan Job = "scan"
	JobCopy Job = "copy"
)

// StartJob registers j on the open library, ended by done. It refuses once
// shutdown began (ErrClosing), while a re-plan waits or runs (ErrReplanning),
// and while another job runs (ErrBusy): a scan replacing the pending plan
// under a copy working through it would copy files the plan no longer holds.
// Plan moves on: anything built over the plan before the job is stale.
func (g *WorkGroup) StartJob(j Job) (done func(), err error) {
	g.mu.Lock()
	switch {
	case g.closed:
		err = ErrClosing
	case g.rewrites.Load() > 0:
		err = ErrReplanning
	case g.job != "":
		err = &BusyError{Job: g.job}
	}
	if err != nil {
		g.mu.Unlock()
		return nil, err
	}
	g.job = j
	g.wg.Add(1)
	g.plan.Add(1)
	g.mu.Unlock()
	g.library.RLock()
	return func() {
		g.library.RUnlock()
		g.mu.Lock()
		g.job = ""
		g.mu.Unlock()
		g.wg.Done()
	}, nil
}

// Running is the job running on the library, "" for none.
func (g *WorkGroup) Running() Job {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.job
}

// Start registers one piece of work, ended by done, or reports false once shutdown began.
func (g *WorkGroup) Start() (done func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, false
	}
	g.wg.Add(1)
	return g.wg.Done, true
}

// StartOnLibrary is Start for work on the open library; the library can't be switched while it runs.
func (g *WorkGroup) StartOnLibrary() (done func(), ok bool) {
	stop, ok := g.Start()
	if !ok {
		return nil, false
	}
	g.library.RLock()
	return func() {
		g.library.RUnlock()
		stop()
	}, true
}

// StartRewrite registers work that replaces the library's whole plan: from
// this call Rewriting is true until the work ends, and Plan moves on. enter,
// called where the work runs, registers it with shutdown, waits for the work
// already on the library, then holds it alone: rewrites run one at a time and
// nothing else runs beside one. Shutdown waits only for a rewrite that
// entered, so one never entered can't hang it; both calls refuse once shutdown
// began (ErrClosing), and StartRewrite refuses while a job runs (ErrBusy).
func (g *WorkGroup) StartRewrite() (enter func() (done func(), err error), err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil, ErrClosing
	}
	if g.job != "" {
		return nil, &BusyError{Job: g.job}
	}
	g.rewrites.Add(1)
	g.plan.Add(1)
	return func() (func(), error) {
		stop, ok := g.Start()
		if !ok {
			g.rewrites.Add(-1)
			return nil, ErrClosing
		}
		g.library.Lock()
		return func() {
			g.library.Unlock()
			g.rewrites.Add(-1)
			stop()
		}, nil
	}, nil
}

// Rewriting reports whether a plan rewrite is waiting or running.
func (g *WorkGroup) Rewriting() bool { return g.rewrites.Load() > 0 }

// Plan counts changes to the plan: a re-plan or job registered, a switch,
// discard or restore. Anything built over the plan when Plan read n is stale
// once it reads more.
func (g *WorkGroup) Plan() uint64 { return g.plan.Load() }

// HoldLibrary takes the library from all work, or reports false while any runs on it.
func (g *WorkGroup) HoldLibrary() (release func(), ok bool) {
	if !g.library.TryLock() {
		return nil, false
	}
	return g.library.Unlock, true
}

// CloseAndWait refuses new work and waits for what is running.
func (g *WorkGroup) CloseAndWait() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.wg.Wait()
}
