// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func newTestList() StageList {
	return NewStageList(
		nil,
		&Stage{Key: "scan", Name: "Scan"},
		&Stage{Key: "metadata", Name: "Metadata", HasBar: true},
	)
}

func TestStageList(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// Every rendered row must fit the given width exactly-or-under, with the
		// elapsed time flush at the right edge for running/done rows.
		{"StageListRowsFitWidth", func(t *testing.T) {
			sl := newTestList()
			sl.Start("scan", "Walking directories with an extremely long label that must truncate somewhere sensible")
			sl.AddTail("some/deeply/nested/dir/IMG_0001.HEIC")

			const width = 60
			for _, line := range strings.Split(sl.View(width, 5), "\n") {
				if w := ansi.StringWidth(line); w > width {
					t.Errorf("row wider than terminal: %d > %d: %q", w, width, line)
				}
			}
		}},
		{"StageListDoneCollapsesTail", func(t *testing.T) {
			sl := newTestList()
			sl.Start("scan", "Scanning")
			sl.AddTail("a.jpg")
			sl.AddTail("b.jpg")
			sl.Done("scan", "Scanned 2 files", "1.2s")

			out := sl.View(80, 10)
			if strings.Contains(out, "a.jpg") {
				t.Errorf("done stage should collapse its tail, got:\n%s", out)
			}
			if !strings.Contains(out, "Scanned 2 files") || !strings.Contains(out, "1.2s") {
				t.Errorf("done row missing summary or elapsed:\n%s", out)
			}
		}},
		{"StageListTailWindow", func(t *testing.T) {
			sl := newTestList()
			sl.Start("scan", "Scanning")
			for _, f := range []string{"1.jpg", "2.jpg", "3.jpg", "4.jpg"} {
				sl.AddTail(f)
			}
			out := sl.View(80, 2)
			if strings.Contains(out, "2.jpg") || !strings.Contains(out, "4.jpg") {
				t.Errorf("tail window should show only the last 2 lines:\n%s", out)
			}
			if sl.HeaderLines() != 2 {
				t.Errorf("HeaderLines = %d, want 2", sl.HeaderLines())
			}
		}},
		{"StageListFinishRemaining", func(t *testing.T) {
			sl := newTestList()
			sl.Start("scan", "Scanning")
			sl.FinishRemaining(true, "")
			if sl.stages[0].state != stateFail {
				t.Errorf("running stage should fail on failed finish")
			}
			if sl.stages[1].state != statePending {
				t.Errorf("pending stage should stay pending on failure")
			}

			sl2 := newTestList()
			sl2.FinishRemaining(false, "ready")
			for _, s := range sl2.stages {
				if s.state != stateDone || s.label != "ready" {
					t.Errorf("stage %s should be done/ready, got state=%d label=%q", s.Key, s.state, s.label)
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
