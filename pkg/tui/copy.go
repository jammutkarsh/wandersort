package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/path"
)

// CopyPlan is what a copy would do, read before it starts.
type CopyPlan struct {
	Files     int
	Bytes     int64
	Free      int64
	FreeKnown bool
	Fits      bool
	Edits     int // review edits applied first
}

// CopyProblem is every file left out for one reason.
type CopyProblem struct {
	Reason string
	Next   string
	Paths  []string
}

// CopyResult is what a finished copy did.
type CopyResult struct {
	Copied, Failed, NotRead int
	Bytes                   int64
	Report                  string // the failure page; "" when nothing failed
	Problems                []CopyProblem
}

// CopyConfig wires the Copy tab to the transfer.
type CopyConfig struct {
	Library string
	Plan    CopyPlan
	// Run copies, calling onStep as each step starts (the execute.Step*
	// names) and onProgress after each file; it blocks until done.
	Run func(onStep func(step string), onProgress func(target string, bytes int64, done, total int)) (CopyResult, error)
	// Cancel stops Run between files.
	Cancel context.CancelFunc
}

// CopyFinishedMsg tells the shell a copy ended, so it can re-read the library.
type CopyFinishedMsg struct{}

type copyStepMsg struct{ step string }

// copyFileMsg is the running totals after a file; a dropped one is made up by
// the next.
type copyFileMsg struct {
	target string
	copied int64
	done   int
}

type copyDoneMsg struct {
	res CopyResult
	err error
}

type copyState int

const (
	copyAsking copyState = iota
	copyRunning
	copyStopping
	copyDone
	copyFailed
)

// maxProblemPaths is how many files per reason the screen names; the page has
// every one.
const maxProblemPaths = 3

// CopyModel is the Copy tab: what a copy would do, the copy itself, and what
// it did.
type CopyModel struct {
	cfg    CopyConfig
	sl     StageList
	events chan tea.Msg
	w, h   int

	state    copyState
	copied   int64 // bytes so far
	files    int
	started  time.Time
	res      CopyResult
	err      error
	choice   int
	showKeys bool
}

var copyKeys = []KeyGroup{
	{"Before", []KeyLine{
		{"enter", "start copying"},
		{"esc", "not now"},
	}},
	{"While copying", []KeyLine{
		{"ctrl+t", "next tab; copying keeps going"},
		{"ctrl+c", "stop between files; the next copy carries on"},
	}},
	{"After", []KeyLine{
		{"1-2", "choose what's next"},
		{"r", "open the report of files left out"},
		{"o", "open the library"},
	}},
}

func NewCopyModel(cfg CopyConfig) CopyModel {
	sl := NewStageList(
		func(cur, total int) string { return humanBytes(int64(cur)) + " / " + humanBytes(int64(total)) },
		&Stage{Key: "space", Name: "Check space"},
		&Stage{Key: "apply", Name: "Apply edits"},
		&Stage{Key: "backup", Name: "Back up library"},
		&Stage{Key: "copy", Name: "Copy & check", HasBar: true},
	)
	return CopyModel{cfg: cfg, sl: sl, events: make(chan tea.Msg, 64)}
}

// Busy is a copy in flight: it must not be interrupted or replaced.
func (m CopyModel) Busy() bool { return m.state == copyRunning || m.state == copyStopping }

func (m CopyModel) Init() tea.Cmd { return m.sl.Init() }

// next waits for the copy goroutine's next report.
func (m CopyModel) next() tea.Msg { return <-m.events }

func (m CopyModel) start() (CopyModel, tea.Cmd) {
	m.state, m.started = copyRunning, time.Now()
	events := m.events
	run := m.cfg.Run
	// one goroutine per copy, ending with it; file reports never block the
	// copy (the totals ride on the next one), so a closed screen can't stall it
	go func() {
		var copied int64
		res, err := run(
			func(step string) { events <- copyStepMsg{step} },
			func(target string, bytes int64, done, _ int) {
				copied += bytes
				select {
				case events <- copyFileMsg{target, copied, done}:
				default:
				}
			},
		)
		events <- copyDoneMsg{res, err}
	}()
	return m, m.next
}

func (m CopyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.sl.SetWidth(msg.Width)
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case copyStepMsg:
		m.stepTo(msg.step)
		return m, m.next
	case copyFileMsg:
		m.copied, m.files = msg.copied, msg.done
		m.sl.AddTail(msg.target)
		return m, tea.Batch(m.sl.SetProgress("copy", int(m.copied), int(m.cfg.Plan.Bytes)), m.next)
	case copyDoneMsg:
		m.res, m.err = msg.res, msg.err
		switch {
		case m.state == copyStopping:
			m.sl.FinishRemaining(true, "stopped")
			m.state = copyFailed
		case msg.err != nil:
			m.sl.FinishRemaining(true, "")
			m.state = copyFailed
		default:
			m.sl.Done("copy", fmt.Sprintf("Copied and checked %s", countWord(msg.res.Copied, "file")), "")
			m.sl.FinishRemaining(false, "done")
			m.state = copyDone
		}
		return m, func() tea.Msg { return CopyFinishedMsg{} }
	}
	return m, m.sl.Update(msg)
}

// stepTo settles the steps before step and starts it.
func (m *CopyModel) stepTo(step string) {
	plan := m.cfg.Plan
	done := map[string]string{
		"space":  fmt.Sprintf("Enough space: %s free, %s needed", humanBytes(plan.Free), humanBytes(plan.Bytes)),
		"apply":  "Applied " + countWord(plan.Edits, "edit"),
		"backup": "Backed up the library",
	}
	if !plan.FreeKnown {
		done["space"] = "Free space unknown; copying anyway"
	}
	if plan.Edits == 0 {
		done["apply"] = "No edits to apply"
	}
	for _, s := range m.sl.stages {
		if s.Key == step {
			m.sl.Start(step, "")
			return
		}
		if s.state != stateDone {
			m.sl.Done(s.Key, done[s.Key], "")
		}
	}
}

func (m CopyModel) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showKeys {
		m.showKeys = false
		return m, nil
	}
	key := k.String()
	switch key {
	case "?":
		m.showKeys = true
		return m, nil
	case "ctrl+c":
		if m.state == copyRunning {
			if m.cfg.Cancel != nil {
				m.cfg.Cancel()
			}
			m.state = copyStopping
			return m, nil
		}
		return m, Left(Leave{Quit: true})
	}
	switch m.state {
	case copyAsking:
		switch key {
		case "enter":
			if m.cfg.Plan.Files == 0 || !m.cfg.Plan.Fits {
				return m, nil
			}
			return m.start()
		case "esc":
			return m, Left(Leave{})
		}
	case copyDone, copyFailed:
		switch key {
		case "r":
			if m.res.Report != "" {
				OpenInViewer(m.res.Report)
			}
		case "o":
			OpenInViewer(m.cfg.Library)
		case "a", "esc":
			return m, Left(Leave{})
		case "up":
			m.choice = 0
		case "down":
			m.choice = 1
		case "1", "2":
			m.choice = int(k.Runes[0] - '1')
		case "enter":
			if m.choice == 0 {
				OpenInViewer(m.cfg.Library)
				return m, nil
			}
			return m, Left(Leave{})
		}
	}
	return m, nil
}

func (m CopyModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	switch m.state {
	case copyAsking:
		b.WriteString(m.askView())
	default:
		b.WriteString(row("  "+m.heading(), "", m.w) + "\n\n")
		b.WriteString(m.sl.View(m.w) + "\n")
		b.WriteString(m.belowStages())
	}
	view := Screen(b.String(), m.footer(), m.h)
	if m.showKeys {
		return KeyHelp(view, copyKeys, m.w, m.h)
	}
	return view
}

func (m CopyModel) askView() string {
	plan := m.cfg.Plan
	var b strings.Builder
	if plan.Files == 0 {
		b.WriteString(row("  "+Text.Bold(true).Render("Nothing to copy"), "", m.w) + "\n\n")
		b.WriteString(row("  "+DimText.Render("Every planned file is already in the library. Add folders to plan more."), "", m.w) + "\n")
		return b.String()
	}
	b.WriteString(row("  "+Text.Bold(true).Render("Copy "+countWord(plan.Files, "file")+" into your library"), "", m.w) + "\n\n")
	b.WriteString(row("  Size", humanBytes(plan.Bytes)+"  ", m.w) + "\n")
	switch {
	case !plan.FreeKnown:
		b.WriteString(row("  Free on this drive  "+DimText.Render("couldn't be read"), "", m.w) + "\n")
	case plan.Fits:
		b.WriteString(row("  Free on this drive  "+OK.Render("✓ enough"), humanBytes(plan.Free)+"  ", m.w) + "\n")
	default:
		b.WriteString(row("  Free on this drive  "+Bad.Render("✗ not enough"), humanBytes(plan.Free)+"  ", m.w) + "\n")
	}
	if plan.Edits > 0 {
		b.WriteString(row("  Your edits  "+DimText.Render("from Organise, applied first"), fmt.Sprint(plan.Edits)+"  ", m.w) + "\n")
	}
	b.WriteString("\n")
	if !plan.Fits {
		b.WriteString(row("  "+Attn.Render("⚠ Free some space on the library's drive, then come back."), "", m.w) + "\n")
		return b.String()
	}
	b.WriteString(row("  "+FaintTxt.Render("Originals stay where they are. Every copy is checked against its original."), "", m.w) + "\n")
	return b.String()
}

func (m CopyModel) heading() string {
	switch {
	case m.state == copyDone && m.res.Failed+m.res.NotRead == 0:
		return OK.Render("✓ All done")
	case m.state == copyDone:
		return Attn.Render("⚠ Done, but " + countWord(m.res.Failed+m.res.NotRead, "file") + " didn't make it in")
	case m.state == copyFailed && m.err != nil:
		return Bad.Render("Copy stopped")
	case m.state == copyStopping:
		return Attn.Render("Stopping after this file…")
	}
	return Text.Bold(true).Render("Copying " + countWord(m.cfg.Plan.Files, "file"))
}

func (m CopyModel) belowStages() string {
	var b strings.Builder
	line := func(s string) { b.WriteString(row("  "+s, "", m.w) + "\n") }
	switch m.state {
	case copyRunning, copyStopping:
		spent := time.Since(m.started).Seconds()
		detail := countWord(m.files, "file") + " checked"
		if spent > 1 && m.copied > 0 {
			detail += fmt.Sprintf(" · %s/s", humanBytes(int64(float64(m.copied)/spent)))
		}
		if left := TimeLeft(m.sl.Remaining("copy")); left != "" {
			detail += " · " + strings.ToLower(left[:1]) + left[1:]
		}
		line(DimText.Render(detail))
		b.WriteString("\n")
		line(DimText.Render("Stopping is safe. The next copy picks up where this one left off."))
	case copyFailed:
		b.WriteString("\n")
		if m.err != nil {
			line(Bad.Render("✗ ") + Text.Render(m.err.Error()))
		}
		if m.res.Copied > 0 {
			line(DimText.Render(countWord(m.res.Copied, "file") + " made it in before it stopped; the next copy carries on."))
		}
	case copyDone:
		b.WriteString("\n")
		for _, p := range m.res.Problems {
			line(Bad.Render("✗ ") + Text.Render(fmt.Sprintf("%s (%d)", p.Reason, len(p.Paths))))
			for _, path := range p.Paths[:min(len(p.Paths), maxProblemPaths)] {
				line("    " + DimText.Render(path))
			}
			if rest := len(p.Paths) - maxProblemPaths; rest > 0 {
				line("    " + FaintTxt.Render(fmt.Sprintf("… %d more in the report", rest)))
			}
			line("  " + Attn.Render(p.Next))
		}
		if len(m.res.Problems) > 0 {
			b.WriteString("\n")
		} else {
			line(DimText.Render("Originals left where they were. Your library is in ") + Text.Render(path.New().RelativeToHome(m.cfg.Library)))
			b.WriteString("\n")
		}
		for i, c := range []string{"Open the library", "Add more folders"} {
			if i == m.choice {
				line(Title.Render(fmt.Sprintf("❯ %d) ", i+1)) + Text.Bold(true).Render(c))
				continue
			}
			line("  " + DimText.Render(fmt.Sprintf("%d) ", i+1)) + Text.Render(c))
		}
	}
	return b.String()
}

func (m CopyModel) footer() string {
	hints := func(h ...string) string { return Footer(strings.Join(h, "   "), m.w) }
	switch m.state {
	case copyAsking:
		if m.cfg.Plan.Files == 0 || !m.cfg.Plan.Fits {
			return hints(KeyHint("esc", "back"), MoreKeys())
		}
		return hints(KeyHint("enter", "start"), KeyHint("esc", "not now"), MoreKeys())
	case copyRunning, copyStopping:
		return hints(KeyHint("ctrl+t", "switch tab"), KeyHint("ctrl+c", "stop"), MoreKeys())
	}
	if m.res.Report != "" {
		return hints(KeyHint("r", "open report"), KeyHint("1-2", "choose"), KeyHint("enter", "go"), MoreKeys())
	}
	return hints(KeyHint("1-2", "choose"), KeyHint("enter", "go"), MoreKeys())
}

// countWord is "1 file", "3 files", "15,481 files".
func countWord(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return Count(n) + " " + word + "s"
}
