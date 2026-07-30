// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// StageList is the Docker-buildkit-style step stack shared by every pipeline
// screen: ` => [i/N] Name` rows, elapsed time right-aligned, the running
// stage nesting a progress bar and live item tail under it.
type StageList struct {
	stages    []*Stage
	idx       map[string]int
	sb        spinnerBar
	fmtCounts func(cur, total int) string // counts next to the bar; nil = "cur/total"
}

// Stage is one row in a StageList. Key must match the logger.PhaseKey value
// the pipeline emits for it.
type Stage struct {
	Key    string
	Name   string
	HasBar bool

	state stageState
	label string // running message, or the done summary
	start time.Time
	dur   string // frozen elapsed once done/failed
	cur   int
	total int
	tail  []string
}

type stageState int

const (
	statePending stageState = iota
	stateRunning
	stateDone
	stateFail
)

// tailKeep bounds each stage's stream buffer; the view only shows a window of
// it, sized to the terminal.
const tailKeep = 400

// NewStageList builds the component. fmtCounts formats the cur/total pair next
// to a running bar (files for scan, bytes for downloads); nil means "cur/total".
func NewStageList(fmtCounts func(cur, total int) string, stages ...*Stage) StageList {
	idx := make(map[string]int, len(stages))
	for i, s := range stages {
		idx[s.Key] = i
	}
	if fmtCounts == nil {
		fmtCounts = func(cur, total int) string { return fmt.Sprintf("%d/%d", cur, total) }
	}
	return StageList{stages: stages, idx: idx, sb: newSpinnerBar(), fmtCounts: fmtCounts}
}

// Init returns the command that starts the spinner ticking — the tick is also
// what keeps the running stage's elapsed time redrawing.
func (sl StageList) Init() tea.Cmd { return sl.sb.spin.Tick }

// Update advances the spinner/progress-bar animations. Call it from the host
// screen's Update for every message; unrelated messages are a no-op.
func (sl *StageList) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	sl.sb, cmd = sl.sb.update(msg)
	return cmd
}

// SetWidth resizes the progress bar to the terminal.
func (sl *StageList) SetWidth(w int) {
	sl.sb.bar.Width = clamp(w-30, 20, 60)
}

// Start marks a stage running with its live label.
func (sl *StageList) Start(key, label string) {
	if s := sl.get(key); s != nil {
		s.state = stateRunning
		s.label = label
		s.start = time.Now()
	}
}

// Done collapses a stage to its summary. elapsed is the pipeline's own
// measurement (logger.ElapsedKey); empty falls back to the time since Start.
func (sl *StageList) Done(key, summary, elapsed string) {
	if s := sl.get(key); s != nil {
		s.state = stateDone
		s.label = summary
		s.tail = nil
		s.dur = elapsed
		if s.dur == "" {
			s.dur = liveElapsed(s.start)
		}
	}
}

// SetProgress drives a stage's bar. Returns the bar animation command.
func (sl *StageList) SetProgress(key string, cur, total int) tea.Cmd {
	s := sl.get(key)
	if s == nil || total <= 0 {
		return nil
	}
	s.cur, s.total = cur, total
	return sl.sb.bar.SetPercent(float64(cur) / float64(total))
}

// AddTail appends one stream line (a file being processed) under the running
// stage.
func (sl *StageList) AddTail(line string) {
	for _, s := range sl.stages {
		if s.state == stateRunning {
			s.tail = append(s.tail, line)
			if len(s.tail) > tailKeep {
				s.tail = s.tail[len(s.tail)-tailKeep:]
			}
			return
		}
	}
}

// FinishRemaining settles every unfinished stage after the pipeline returns:
// on failure the running stage turns red; on success everything left flips to
// done, labelled defaultLabel if it never reported (e.g. an already-cached
// dependency).
func (sl *StageList) FinishRemaining(failed bool, defaultLabel string) {
	for _, s := range sl.stages {
		switch {
		case failed && s.state == stateRunning:
			s.state = stateFail
			s.dur = liveElapsed(s.start)
			s.tail = nil
		case !failed && s.state != stateDone:
			s.state = stateDone
			s.tail = nil
			if s.label == "" {
				s.label = defaultLabel
			}
			if s.dur == "" {
				s.dur = liveElapsed(s.start)
			}
		}
	}
}

// HeaderLines is how many rows the stage headers and bar occupy — the host
// subtracts it (plus its own chrome) from the terminal height to budget the
// tail window it passes to View.
func (sl StageList) HeaderLines() int {
	n := len(sl.stages)
	for _, s := range sl.stages {
		if s.state == stateRunning && s.HasBar && s.total > 0 {
			n++
		}
	}
	return n
}

// View renders the stack at the given width. tailBudget is how many stream
// rows may render under the running stage — pass what's left of the terminal
// so a tall window shows a long live tail.
func (sl StageList) View(width, tailBudget int) string {
	var b strings.Builder
	n := len(sl.stages)
	for i, s := range sl.stages {
		head := fmt.Sprintf("[%d/%d] %-14s", i+1, n, s.Name)
		switch s.state {
		case statePending:
			b.WriteString(row(FaintTxt.Render(" => "+head), "", width))
		case stateRunning:
			left := Title.Render(" => ") + Text.Bold(true).Render(head) + " " +
				sl.sb.spin.View() + " " + DimText.Render(s.label)
			b.WriteString(row(left, FaintTxt.Render(liveElapsed(s.start)), width))
			if s.HasBar && s.total > 0 {
				b.WriteString("\n")
				b.WriteString(row(Title.Render(" => => ")+sl.sb.bar.View()+" "+
					FaintTxt.Render(sl.fmtCounts(s.cur, s.total)), "", width))
			}
			b.WriteString(sl.viewTail(s, width, tailBudget))
		case stateDone:
			left := OK.Render(" => ") + Text.Render(head) + "   " + DimText.Render(s.label)
			b.WriteString(row(left, FaintTxt.Render(s.dur), width))
		case stateFail:
			left := Bad.Render(" => ") + Text.Render(head) + "   " + Bad.Render(nonEmpty(s.label, "failed"))
			b.WriteString(row(left, FaintTxt.Render(s.dur), width))
		}
		if i < n-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (sl StageList) viewTail(s *Stage, width, budget int) string {
	if budget <= 0 || len(s.tail) == 0 {
		return ""
	}
	tail := s.tail
	if len(tail) > budget {
		tail = tail[len(tail)-budget:]
	}
	var b strings.Builder
	mark := FaintTxt.Render(" => => ")
	for _, l := range tail {
		b.WriteString("\n")
		b.WriteString(row(mark+DimText.Render(l), "", width))
	}
	return b.String()
}

func (sl *StageList) get(key string) *Stage {
	if i, ok := sl.idx[key]; ok {
		return sl.stages[i]
	}
	return nil
}

// row lays out one full-width line: left content truncated to fit, right
// suffix (the elapsed time) aligned to the terminal edge — the Docker look.
func row(left, right string, width int) string {
	if width <= 0 {
		if right == "" {
			return left
		}
		return left + "  " + right
	}
	if right == "" {
		return ansi.Truncate(left, width, "…")
	}
	rw := ansi.StringWidth(right)
	left = ansi.Truncate(left, width-rw-2, "…")
	pad := max(width-ansi.StringWidth(left)-rw, 1)
	return left + strings.Repeat(" ", pad) + right
}

func liveElapsed(start time.Time) string {
	if start.IsZero() {
		return ""
	}
	return time.Since(start).Round(100 * time.Millisecond).String()
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
