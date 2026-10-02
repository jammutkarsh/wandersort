package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jammutkarsh/wandersort/pkg/human"
)

// StageList is the stage stack every pipeline screen shares: status, elapsed time, and the running stage's bar.
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
	tail  string // the item being worked on
}

type stageState int

const (
	statePending stageState = iota
	stateRunning
	stateDone
	stateFail
)

// NewStageList builds the component. fmtCounts formats the cur/total pair next
// to a running bar (files for scan, bytes for downloads); nil means "cur/total".
func NewStageList(fmtCounts func(cur, total int) string, stages ...*Stage) StageList {
	idx := make(map[string]int, len(stages))
	for i, s := range stages {
		idx[s.Key] = i
	}
	if fmtCounts == nil {
		fmtCounts = func(cur, total int) string { return human.Count(cur) + " / " + human.Count(total) }
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

func (sl *StageList) SetWidth(w int) {
	sl.sb.bar.Width = clamp(w-56, 16, 40)
}

// SetLabel updates a running stage's message without resetting its clock.
func (sl *StageList) SetLabel(key, label string) {
	if s := sl.get(key); s != nil && s.state == stateRunning {
		s.label = label
	}
}

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
		s.tail = ""
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

// AddTail names the item (a file) the running stage is on.
func (sl *StageList) AddTail(line string) {
	for _, s := range sl.stages {
		if s.state == stateRunning {
			s.tail = line
			return
		}
	}
}

// etaAfter is how long a stage runs before its time left is worth guessing.
const etaAfter = 3 * time.Second

// Remaining guesses the running stage's time left from its bar's rate; 0 with nothing to go on.
func (sl StageList) Remaining(key string) time.Duration {
	s := sl.get(key)
	if s == nil || s.state != stateRunning || s.cur <= 0 || s.total <= s.cur {
		return 0
	}
	spent := time.Since(s.start)
	if spent < etaAfter {
		return 0
	}
	return time.Duration(float64(spent) * float64(s.total-s.cur) / float64(s.cur))
}

// TimeLeft says a Remaining duration the way a person would.
func TimeLeft(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < time.Minute:
		return "Less than a minute left"
	case d < 2*time.Minute:
		return "About a minute left"
	case d < time.Hour:
		return fmt.Sprintf("About %d minutes left", int(d.Round(time.Minute)/time.Minute))
	}
	return fmt.Sprintf("About %.1f hours left", d.Hours())
}

// FinishRemaining settles every unfinished stage once the pipeline returns.
func (sl *StageList) FinishRemaining(failed bool, defaultLabel string) {
	for _, s := range sl.stages {
		switch {
		case failed && s.state == stateRunning:
			s.state = stateFail
			s.dur = liveElapsed(s.start)
			s.tail = ""
		case !failed && s.state != stateDone:
			s.state = stateDone
			s.tail = ""
			if s.label == "" {
				s.label = defaultLabel
			}
			if s.dur == "" {
				s.dur = liveElapsed(s.start)
			}
		}
	}
}

// Summary is every finished stage's one-line result with its elapsed time —
// what a completed run leaves behind for a screen that outlives it.
func (sl StageList) Summary() []string {
	var out []string
	for _, s := range sl.stages {
		if s.state == stateDone && s.label != "" {
			out = append(out, s.label+"  "+s.dur)
		}
	}
	return out
}

// View renders the stack.
func (sl StageList) View(width int) string {
	nameW := 0
	for _, s := range sl.stages {
		nameW = max(nameW, ansi.StringWidth(s.Name))
	}
	under := strings.Repeat(" ", nameW+6) // lines under a stage start below its label
	var rows []string
	for _, s := range sl.stages {
		name := s.Name + strings.Repeat(" ", nameW-ansi.StringWidth(s.Name))
		switch s.state {
		case statePending:
			rows = append(rows, row("  "+FaintTxt.Render("○ "+name), "", width))
		case stateRunning:
			left := "  " + sl.sb.spin.View() + Text.Bold(true).Render(name) + "  "
			if s.HasBar && s.total > 0 {
				left += sl.sb.bar.View() + "  " + FaintTxt.Render(sl.fmtCounts(s.cur, s.total))
			} else {
				left += DimText.Render(s.label)
			}
			rows = append(rows, row(left, FaintTxt.Render(liveElapsed(s.start)), width))
			if s.tail != "" {
				rows = append(rows, row(under+FaintTxt.Render(s.tail), "", width))
			}
		case stateDone:
			rows = append(rows, row("  "+OK.Render("✓")+" "+Text.Render(nonEmpty(s.label, s.Name)), FaintTxt.Render(s.dur), width))
		case stateFail:
			rows = append(rows, row("  "+Bad.Render("✗")+" "+Text.Render(name)+"  "+Bad.Render(nonEmpty(s.label, "failed")),
				FaintTxt.Render(s.dur), width))
		}
	}
	return strings.Join(rows, "\n")
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
