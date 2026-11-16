package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/jammutkarsh/wandersort/pkg/config"
	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/lock"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// Session is one process's hold on at most one library: its database, the
// output lock that keeps other processes out, and the work running on it.
// Open, Close, Switch and SaveSettings belong to one goroutine; work started
// through Work may run on others.
type Session struct {
	Config *config.Configuration
	Log    logger.Logger
	// LogFile is this process's log, buffered in memory until a library opens
	// (or a warning) persists it. Nil means no file log.
	LogFile *logger.File
	// Work is background work on the library; shutdown waits for it before
	// closing the database.
	Work WorkGroup

	db   *db.DB
	lock *lock.Lock
}

// DB is the open library's database, or nil while none is open. A getter, not
// a field, so only Open and Close change the handle, always with the lock.
func (s *Session) DB() *db.DB { return s.db }

// LibraryExists reports whether the output folder already holds a library
// (its database file exists). Opens nothing.
func (s *Session) LibraryExists() bool {
	_, err := os.Stat(s.Config.AppDBPath)
	return err == nil
}

// Open opens the output folder as a library, once per session: check it may
// be one, take the output lock, open the database, load its settings, then
// remember it. In that order, a refused folder or a lost lock race writes
// nothing into the folder. A library another process holds fails with a
// *lock.AlreadyRunningError in the chain.
func (s *Session) Open(ctx context.Context) error {
	if s.db != nil {
		return nil
	}
	// From here the run touches (or tried to touch) user data: keep its log.
	s.LogFile.Persist()
	outputDir := s.Config.OutputDir()
	if err := config.CheckLibrary(outputDir); err != nil {
		return err
	}
	l, err := lock.AcquireOutput(outputDir)
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	appDB, err := db.New(ctx, s.Config.AppDBPath, s.Log)
	if err != nil {
		l.Unlock()
		return fmt.Errorf("app db: %w", err)
	}
	// The library's own settings (defaults if never saved). Read before
	// publishing the handle, so a failure leaves the library unopened.
	settings, err := config.LoadSettings(ctx, appDB)
	if err != nil {
		appDB.Close()
		l.Unlock()
		return err
	}
	s.db, s.lock = appDB, l
	s.Config.Settings = settings
	// only a real library goes in the recent list
	if err := s.Config.Remember(outputDir); err != nil {
		s.Log.Warn("could not record this library as recently used", "error", err)
	}
	return nil
}

// Close flushes and closes the open library and releases its lock; with none
// open it does nothing.
func (s *Session) Close() {
	// a failed Close can leave WAL/SHM locked (locking_mode=EXCLUSIVE); log it
	if s.db != nil {
		if err := s.db.Writer.Flush(); err != nil {
			s.Log.Error("some writes were lost before closing", "error", err)
		}
		s.Log.Info("Closing databases")
		if err := s.db.Close(); err != nil {
			s.Log.Error("failed to close app database", "error", err)
		}
	}
	s.lock.Unlock() // nil-safe; after Close, so no other process opens the database mid-close
	s.db, s.lock = nil, nil
}

// SaveSettings writes settings into the library at outputDir, opening it
// first if none is open; a settings-first session picks its library here.
func (s *Session) SaveSettings(ctx context.Context, outputDir string, settings config.Settings) error {
	if s.db == nil {
		s.Config.SetOutput(outputDir)
		if err := s.Open(ctx); err != nil {
			return err
		}
	}
	if err := config.SaveSettings(ctx, s.db, settings); err != nil {
		return err
	}
	s.Config.Settings = settings
	return nil
}

// Switch makes dir the session's library. An organised folder opens with its
// own settings; any other starts a new library with the current ones. If dir
// can't be opened, or a new library can't take the settings, the session is
// left as it was, the old library reopened only if one was open.
func (s *Session) Switch(ctx context.Context, dir string) error {
	old := s.Config.OutputDir()
	if dir == old {
		return nil
	}
	if err := config.CheckLibrary(dir); err != nil {
		return err
	}
	release, ok := s.Work.HoldLibrary()
	if !ok {
		return errors.New("busy with this library; change it when it's done")
	}
	defer release()
	_, statErr := os.Stat(filepath.Join(dir, filepath.Base(s.Config.AppDBPath)))
	isNew := statErr != nil
	carry := s.Config.Settings
	wasOpen := s.db != nil

	s.Close()
	s.Config.SetOutput(dir)
	err := s.Open(ctx)
	if err == nil && isNew {
		err = s.SaveSettings(ctx, dir, carry)
	}
	if err == nil {
		return nil
	}
	s.Close()
	s.Config.SetOutput(old)
	s.Config.Settings = carry
	if !wasOpen {
		return err
	}
	return errors.Join(err, s.Open(ctx))
}

// Restore puts the library's backup back over its database (see Restore),
// holding the output lock meanwhile. The session must have no library open:
// a restore refuses any open connection, db.ErrInUse.
func (s *Session) Restore(ctx context.Context) error {
	s.LogFile.Persist()
	l, err := lock.AcquireOutput(s.Config.OutputDir())
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer l.Unlock()
	return Restore(ctx, s.Config.AppDBPath, s.Log)
}

// RememberSources records the folders a scan walked; the first given ends up newest.
func (s *Session) RememberSources(roots []string) {
	for _, root := range slices.Backward(roots) {
		if err := s.Config.RememberSource(root); err != nil {
			s.Log.Warn("could not record a scanned folder as recently used", "error", err)
			return
		}
	}
}

// State answers "what can the user do next?" in one place. Exists and Edits
// are read without opening anything; Open, Planned and Placed stay zero until
// the session opens the library (which takes the lock and may create it).
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
	st := State{Exists: s.LibraryExists()}

	// no plan to replay onto: this only counts the journal
	if d, err := vfs.OpenDraft(s.Config.OutputDir(), nil); err != nil {
		st.Edits = 1 // unreadable: something is there, and execute will say what
	} else {
		st.Edits = len(d.Edits())
	}

	if s.db == nil {
		return st
	}
	st.Open = true
	planned, _, err := execute.Pending(ctx, s.db)
	st.Planned = planned
	if err != nil {
		// A library we can't count is still a library; the caller keeps it
		// reachable and the screen behind it reports the real error.
		s.Log.Warn("could not count the planned files", "error", err)
		st.Planned = 1
	}
	if st.Placed, err = s.db.PlacedCount(ctx); err != nil {
		s.Log.Warn("could not count the placed files", "error", err)
	}
	return st
}

// WorkGroup tracks background work so shutdown can wait for it; work that
// would start after shutdown began is refused instead.
type WorkGroup struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	// library is read-held by work on the open library and write-held while it is swapped
	library sync.RWMutex
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
