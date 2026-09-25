// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func TestReadState(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// Reading the state must never be the thing that creates a library:
		// the shell does it at startup, and a session that only looks around
		// writes nothing outside the logs.
		{"ReadsNothingIntoExistence", func(t *testing.T) {
			a := &app{Config: testConfig(t), Log: logger.NewNoopLogger()}
			s := a.readState(context.Background())
			if s.Exists || s.Open {
				t.Errorf("state = %+v, want an empty folder", s)
			}
			if _, err := os.Stat(a.Config.AppDBPath); !os.IsNotExist(err) {
				t.Error("reading the state created a database")
			}
			if s.CanReview() {
				t.Error("CanReview with no library at all")
			}
		}},
		// Before the library is open, the file being there is the whole
		// answer — finding out for real means opening it, which is what the
		// caller is deciding whether to do.
		{"AClosedLibraryIsAnsweredByItsFile", func(t *testing.T) {
			a := &app{Config: testConfig(t), Log: logger.NewNoopLogger()}
			fakeProposal(t, a)
			s := a.readState(context.Background())
			if !s.Exists || s.Open || s.Planned != 0 {
				t.Errorf("state = %+v, want exists and shut", s)
			}
			if !s.CanReview() {
				t.Error("a library on disk should be reviewable before it is opened")
			}
		}},
		// Once it is open the count is the answer, so a library with nothing
		// left to plan stops claiming to have something to review.
		{"AnOpenLibraryIsAnsweredByItsCount", func(t *testing.T) {
			a := &app{Config: testConfig(t), Log: logger.NewNoopLogger()}
			seedProposal(t, a)
			if err := a.openLibrary(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer a.closeDBs()

			s := a.readState(context.Background())
			if !s.Open || s.Planned != 1 {
				t.Fatalf("state = %+v, want one planned file", s)
			}
			if !s.CanReview() {
				t.Error("a planned file should be reviewable")
			}

			if _, err := a.AppDB.SQL.Exec(`UPDATE file_registry SET placed = 1`); err != nil {
				t.Fatal(err)
			}
			if s := a.readState(context.Background()); s.Planned != 0 || s.CanReview() {
				t.Errorf("state = %+v, want nothing left to review once everything is placed", s)
			}
		}},
		{"CountsTheReviewEditsWaiting", func(t *testing.T) {
			a := &app{Config: testConfig(t), Log: logger.NewNoopLogger()}
			fakeProposal(t, a)
			seedDraft(t, a.Config.OutputDir(),
				vfs.Edit{Seq: 1, Op: vfs.OpRename, Node: 1, From: "03", To: "Goa"},
				vfs.Edit{Seq: 2, Op: vfs.OpDrop, Nodes: []int64{2}})
			s := a.readState(context.Background())
			if s.Edits != 2 || !s.HasEdits() {
				t.Errorf("state = %+v, want two edits waiting", s)
			}
		}},
		// An unreadable draft still means something is waiting: execute is
		// where that surfaces, and the user needs telling either way rather
		// than being told there is nothing to apply.
		{"AnUnreadableDraftStillCounts", func(t *testing.T) {
			a := &app{Config: testConfig(t), Log: logger.NewNoopLogger()}
			fakeProposal(t, a)
			// A bad line that is not the last one: a torn final line is a
			// crash mid-append and heals itself, this is corruption.
			draft := filepath.Join(a.Config.OutputDir(), vfs.DraftFileName)
			if err := os.WriteFile(draft, []byte("{not json\n{\"seq\":2,\"op\":\"drop\"}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := vfs.OpenDraft(a.Config.OutputDir(), nil); err == nil {
				t.Fatal("this draft should be unreadable, or the test proves nothing")
			}
			if s := a.readState(context.Background()); !s.HasEdits() {
				t.Errorf("state = %+v, want the draft counted", s)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// seedDraft writes edits as the review's journal in dir, the way a review
// session left them, without needing a plan they apply to.
func seedDraft(t *testing.T, dir string, edits ...vfs.Edit) {
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
