package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// stubScreen is a minimal Tab that records what it was sent, standing in for
// a real screen the scan hands control to.
type stubScreen struct {
	view    string
	initCmd tea.Cmd
	msgs    []tea.Msg
}

func (s *stubScreen) Init() tea.Cmd { return s.initCmd }

func (s *stubScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	s.msgs = append(s.msgs, msg)
	return s, nil
}

func (s *stubScreen) View() string { return s.view }

func (s *stubScreen) Busy() bool { return false }

func TestScanModel(t *testing.T) {
	reviewNext := func() (Tab, error) { return &stubScreen{view: "review"}, nil }

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		// A finished plan asks what's next; the prefetched review is handed to
		// the shell to keep, not opened over the question.
		{"PlanReadyAsksWhatNext", func(t *testing.T) {
			m := NewScanModel(ScanConfig{ReviewNext: reviewNext})
			prefetched := &stubScreen{view: "review"}

			next, cmd := m.Update(reviewReadyMsg{model: prefetched})
			for _, msg := range flattenCmd(cmd) {
				if sm, ok := msg.(SwitchMsg); !ok || sm.Next != Tab(prefetched) || sm.Open {
					t.Errorf("the prefetch should be handed over unopened, got %#v", msg)
				}
			}
			next, cmd = next.(ScanModel).Update(scanDoneMsg{})
			if cmd != nil {
				t.Errorf("finishing must not leave or switch by itself, got %v", flattenCmd(cmd))
			}
			if v := ansi.Strip(next.View()); !strings.Contains(v, "Plan ready") || !strings.Contains(v, "1) Look over the folders") {
				t.Errorf("want the plan-ready choice:\n%s", v)
			}
		}},
		{"ChoicesDoWhatTheySay", func(t *testing.T) {
			done := func() ScanModel {
				m := NewScanModel(ScanConfig{ReviewNext: reviewNext})
				next, _ := m.Update(reviewReadyMsg{model: &stubScreen{}})
				next, _ = next.(ScanModel).Update(scanDoneMsg{})
				return next.(ScanModel)
			}
			pick := func(m ScanModel, key string) []tea.Msg {
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
				_, cmd := next.(ScanModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
				return flattenCmd(cmd)
			}
			if msgs := pick(done(), "1"); len(msgs) != 1 || msgs[0] != (OpenReviewMsg{}) {
				t.Errorf("1 = %v, want OpenReviewMsg", msgs)
			}
			if msgs := pick(done(), "2"); len(msgs) != 1 || msgs[0] != (OpenCopyMsg{}) {
				t.Errorf("2 = %v, want OpenCopyMsg", msgs)
			}
			if msgs := pick(done(), "3"); len(msgs) != 1 || msgs[0] != (Leave{}) {
				t.Errorf("3 = %v, want a plain Leave (back to the folder input)", msgs)
			}
		}},
		// Picking the folders before the prefetch lands opens them when it does.
		{"PickBeforePrefetchOpensOnArrival", func(t *testing.T) {
			m := NewScanModel(ScanConfig{ReviewNext: reviewNext})
			next, _ := m.Update(scanDoneMsg{})
			next, cmd := next.(ScanModel).Update(tea.KeyMsg{Type: tea.KeyEnter})
			if cmd != nil {
				t.Fatalf("nothing to open yet, got %v", flattenCmd(cmd))
			}
			if v := ansi.Strip(next.View()); !strings.Contains(v, "opening…") {
				t.Errorf("want the pick shown as opening:\n%s", v)
			}
			landed := &stubScreen{view: "review"}
			_, cmd = next.(ScanModel).Update(reviewReadyMsg{model: landed})
			msgs := flattenCmd(cmd)
			if len(msgs) != 1 || msgs[0] != (SwitchMsg{Next: landed, Open: true}) {
				t.Errorf("arrival should hand over and open, got %#v", msgs)
			}
		}},
		// Warn-once-then-act: the first ctrl+c cancels and says what that
		// costs, the second gives up on a pipeline that won't unwind. Without
		// the second, a wedged phase leaves the screen unquittable.
		{"SecondCtrlCQuitsAWedgedPipeline", func(t *testing.T) {
			cancelled := 0
			m := NewScanModel(ScanConfig{Cancel: func() { cancelled++ }})

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			m = next.(ScanModel)
			if cmd != nil {
				t.Fatal("the first ctrl+c should cancel and wait, not quit")
			}
			if cancelled != 1 || !m.Cancelled() {
				t.Fatalf("first ctrl+c should cancel the pipeline: calls=%d cancelled=%v", cancelled, m.Cancelled())
			}
			if v := ansi.Strip(m.View()); !strings.Contains(v, "press ctrl+c again to quit now") {
				t.Errorf("want the warning above the footer:\n%s", v)
			}

			// The screen asks to end the session; it must not end the
			// program itself, which would take the other tabs with it.
			_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			l, left := leaveOf(cmd)
			if !left || !l.Quit {
				t.Errorf("the second ctrl+c should ask to quit, got %v", flattenCmd(cmd))
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

// The scan screen shows one row per workflow phase and no others: a row with
// no phase behind it never gets an event and sits pending forever (the old
// Score row, after duplicate election moved into vfs).
func TestScanModelStagesMatchWorkflowPhases(t *testing.T) {
	var keys []string
	for _, s := range NewScanModel(ScanConfig{}).sl.stages {
		keys = append(keys, s.Key)
	}
	if want := []string{"scan", "metadata", "vfs"}; !slices.Equal(keys, want) {
		t.Errorf("stages = %v, want %v", keys, want)
	}
}
