package cli

import (
	"context"
	"os"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
)

// libraryState answers "what can the user do next?" in one place. Exists and
// Edits are read without opening anything; Open and Planned stay zero until
// this session opens the library (which takes the lock and may create it).
type libraryState struct {
	// Exists is "the output folder already holds a library". Known from a
	// stat, without opening anything.
	Exists bool
	// Edits is how many review edits wait in the draft for execute. An
	// unreadable draft counts as one, so the user still hears about it.
	Edits int

	// Open is whether this session has the library open; the fields below are
	// zero until it does.
	Open bool
	// Planned is how many files are proposed and not yet placed.
	Planned int
}

// CanReview reports whether there is a plan worth reviewing: "probably" while
// the library is shut, the planned count once it is open.
func (s libraryState) CanReview() bool {
	if s.Open {
		return s.Planned > 0
	}
	return s.Exists
}

// HasEdits reports whether review edits are waiting.
func (s libraryState) HasEdits() bool { return s.Edits > 0 }

// readState reads what the app can cheaply know about the library. Opens,
// locks and writes nothing.
func (a *app) readState(ctx context.Context) libraryState {
	s := libraryState{Exists: a.libraryExists()}

	// no plan to replay onto: this only counts the journal
	if d, err := vfs.OpenDraft(a.Config.OutputDir(), nil); err != nil {
		s.Edits = 1 // unreadable: something is there, and execute will say what
	} else {
		s.Edits = len(d.Edits())
	}

	if a.AppDB == nil {
		return s
	}
	s.Open = true
	planned, _, err := execute.Pending(ctx, a.AppDB)
	s.Planned = planned
	if err != nil {
		// A library we can't count is still a library; the tab stays reachable
		// and the screen behind it reports the real error.
		a.Log.Warn("could not count the planned files", "error", err)
		s.Planned = 1
	}
	return s
}

// libraryExists reports whether the output folder already holds a library
// (its database file exists).
func (a *app) libraryExists() bool {
	_, err := os.Stat(a.Config.AppDBPath)
	return err == nil
}
