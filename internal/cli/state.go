// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"os"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
)

// libraryState is what the app knows about the library right now — enough to
// answer "what can the user do next?" in one place.
//
// It used to be answered by eighteen separate predicates spread across four
// packages: five spellings of "is there a plan" alone (one method and four
// inline os.Stat calls with four different error messages), two of "are there
// review edits" that disagreed about an unreadable draft, and a tab bar that
// ran a filesystem syscall from View() on every rendered frame. Each of them
// was right on its own and they drifted as a set; a new fact — issue 21's
// unfinished copy — would have been a nineteenth.
//
// The split between the two halves below is not tidiness: opening a library
// takes the exclusive output lock and creates the database if it isn't there,
// and a session that only looks around must do neither (see app.openLibrary).
// So the cheap half is always true, and the rest is zero until this session
// has actually opened the library.
type libraryState struct {
	// Exists is "the output folder already holds a library". Known from a
	// stat, without opening anything.
	Exists bool
	// Edits is how many review edits are waiting in the draft for execute. An
	// unreadable draft counts as one: execute is where that surfaces, and the
	// user still needs telling that something is waiting.
	Edits int

	// Open is whether this session has the library open; the fields below are
	// zero until it does.
	Open bool
	// Planned is how many files are proposed and not yet placed — the number
	// a review has anything to say about, and the number execute would
	// transfer.
	Planned int
}

// CanReview reports whether there is a plan worth opening the review over.
// Before the library is open the honest answer is "there is a library, so
// probably" — finding out for real means opening it, which is the very thing
// the caller is deciding whether to do. Once it is open, the count is the
// answer, so a library with everything already organized stops claiming to
// have something to review.
func (s libraryState) CanReview() bool {
	if s.Open {
		return s.Planned > 0
	}
	return s.Exists
}

// HasEdits reports whether leaving the review has anything to say about what
// happens to those edits next.
func (s libraryState) HasEdits() bool { return s.Edits > 0 }

// readState reads everything the app can cheaply know about the library. Safe
// to call before anything is open: it opens nothing, locks nothing and writes
// nothing.
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
	if err := a.AppDB.SQL.GetContext(ctx, &s.Planned,
		`SELECT count(*) FROM virtual_fs_entries ve WHERE `+db.PendingTransfer("ve.file_id")); err != nil {
		// A library we can't count is still a library; the tab stays reachable
		// and the screen behind it reports the real error.
		a.Log.Warn("could not count the planned files", "error", err)
		s.Planned = 1
	}
	return s
}

// libraryExists reports whether the output folder already holds a library.
// The database file is the proxy — nothing else writes one, and answering it
// for real means opening it. This is the one spelling of that question; every
// command keeps its own wording for what to do about the answer.
func (a *app) libraryExists() bool {
	_, err := os.Stat(a.Config.AppDBPath)
	return err == nil
}
