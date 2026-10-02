package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
)

func newTestCopy(plan CopyPlan, run func(func(string), func(string, int64, int, int)) (CopyResult, error)) CopyModel {
	m := NewCopyModel(CopyConfig{Library: "/lib", Plan: plan, Run: run})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return next.(CopyModel)
}

// runToEnd presses enter and feeds the copy's reports back in until it ends.
func runToEnd(t *testing.T, m CopyModel) CopyModel {
	t.Helper()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(CopyModel)
	for m.Busy() {
		next, _ = m.Update(m.next())
		m = next.(CopyModel)
	}
	return m
}

var fits = CopyPlan{Files: 2, Bytes: 200, Free: 1 << 30, FreeKnown: true, Fits: true, Edits: 1}

func TestCopyRunsStepsInOrder(t *testing.T) {
	m := newTestCopy(fits, func(step func(string), file func(string, int64, int, int)) (CopyResult, error) {
		for _, s := range []string{execute.StepSpace, execute.StepApply, execute.StepBackup, execute.StepCopy} {
			step(s)
		}
		file("2024/a.jpg", 100, 1, 2)
		file("2024/b.jpg", 100, 2, 2)
		return CopyResult{Copied: 2, Bytes: 200}, nil
	})
	m = runToEnd(t, m)
	view := ansi.Strip(m.View())
	for _, want := range []string{"All done", "Enough space", "Applied 1 edit", "Backed up", "Copied and checked 2 files", "1) Open the library"} {
		if !strings.Contains(view, want) {
			t.Errorf("finished copy is missing %q:\n%s", want, view)
		}
	}
}

func TestCopyNamesWhatWasLeftOut(t *testing.T) {
	m := newTestCopy(fits, func(step func(string), _ func(string, int64, int, int)) (CopyResult, error) {
		step(execute.StepCopy)
		return CopyResult{Copied: 1, Failed: 1, Report: "/logs/run.html", Problems: []CopyProblem{
			{Reason: "Original is gone", Next: "Plug in the drive it was on.", Paths: []string{"/Volumes/SD/IMG_1.JPG"}},
		}}, nil
	})
	m = runToEnd(t, m)
	view := strings.ReplaceAll(ansi.Strip(m.View()), "\u00a0", " ")
	for _, want := range []string{"didn't make it in", "Original is gone (1)", "/Volumes/SD/IMG_1.JPG", "Plug in the drive", "r open report"} {
		if !strings.Contains(view, want) {
			t.Errorf("copy with failures is missing %q:\n%s", want, view)
		}
	}
}

func TestCopyRefusesWhatWontFit(t *testing.T) {
	plan := fits
	plan.Fits = false
	m := newTestCopy(plan, func(func(string), func(string, int64, int, int)) (CopyResult, error) {
		t.Error("a plan that doesn't fit must not start")
		return CopyResult{}, nil
	})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "not enough") {
		t.Errorf("want the space refusal:\n%s", v)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(CopyModel).Busy() {
		t.Error("enter started a copy that doesn't fit")
	}
}

func TestCopyCtrlCStopsBetweenFiles(t *testing.T) {
	cancelled := 0
	release := make(chan struct{})
	m := NewCopyModel(CopyConfig{
		Plan: fits, Cancel: func() { cancelled++; close(release) },
		Run: func(step func(string), _ func(string, int64, int, int)) (CopyResult, error) {
			step(execute.StepCopy)
			<-release
			return CopyResult{Copied: 1}, errors.New("context canceled")
		},
	})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.Update(next.(CopyModel).next()) // the copy step
	next, cmd := next.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || cancelled != 1 || !next.(CopyModel).Busy() {
		t.Fatalf("first ctrl+c should cancel and wait: cmd=%v cancelled=%d", cmd, cancelled)
	}
	next, _ = next.Update(next.(CopyModel).next())
	if v := ansi.Strip(next.View()); next.(CopyModel).Busy() || !strings.Contains(v, "1 file made it in") {
		t.Errorf("a stopped copy should say what made it in:\n%s", v)
	}
}
