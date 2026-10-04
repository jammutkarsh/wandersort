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
			for _, line := range strings.Split(sl.View(width), "\n") {
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

			out := sl.View(80)
			if strings.Contains(out, "a.jpg") {
				t.Errorf("done stage should collapse its tail, got:\n%s", out)
			}
			if !strings.Contains(out, "Scanned 2 files") || !strings.Contains(out, "1.2s") {
				t.Errorf("done row missing summary or elapsed:\n%s", out)
			}
		}},
		{"StageListShowsOnlyTheCurrentItem", func(t *testing.T) {
			sl := newTestList()
			sl.Start("scan", "Scanning")
			for _, f := range []string{"1.jpg", "2.jpg", "3.jpg", "4.jpg"} {
				sl.AddTail(f)
			}
			out := sl.View(80)
			if strings.Contains(out, "3.jpg") || !strings.Contains(out, "4.jpg") {
				t.Errorf("only the item being worked on should show:\n%s", out)
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

func TestTruncateMiddle(t *testing.T) {
	long := "~/Pictures/trips/2025/winter/day-two/IMG_0042.HEIC"
	tests := []struct {
		name, in, want string
		w              int
	}{
		{"fits", "~/a/b.jpg", "~/a/b.jpg", 20},
		{"no width", long, long, 0},
		{"keeps both ends", long, "~/Pictures…ay-two/IMG_0042.HEIC", 31},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateMiddle(tt.in, tt.w)
			if got != tt.want {
				t.Errorf("truncateMiddle(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.want)
			}
			if tt.w > 0 && ansi.StringWidth(got) > tt.w {
				t.Errorf("width %d > %d", ansi.StringWidth(got), tt.w)
			}
		})
	}
}
