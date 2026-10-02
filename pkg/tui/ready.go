package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/volume"
)

// InstallProgressMsg reports one dependency: bytes so far, or Ready.
type InstallProgressMsg struct {
	Phase       string
	Done, Total int64
	Ready       bool
}

// RetryMsg says a try failed; the screen closes Go to retry, after Wait or at once on enter.
type RetryMsg struct {
	Failed map[string]string // why each dependency that failed did, by phase
	Next   int               // the try about to run
	Tries  int               // tries allowed in all
	Wait   time.Duration     // how long before the next try starts on its own
	Go     chan<- struct{}
}

// DepsFailedMsg says the last try failed; the app ends on the next key.
type DepsFailedMsg struct {
	Failed map[string]string // why each dependency that failed did, by phase
	Tries  int
}

// ReadyItem is one dependency the getting-ready screen lists, in install order.
type ReadyItem struct {
	Phase string
	Label string
}

type retryTickMsg struct{ gen int }

type readyRow struct {
	ReadyItem
	start       time.Time
	done, total int64
	ready       bool
	failed      string // why the last try failed; empty while checking
}

// ReadyModel is a session's first screen: the dependencies, their downloads, and a wait between tries.
type ReadyModel struct {
	rows []readyRow
	sb   spinnerBar
	w, h int

	// waiting holds the retry gate while a failed try counts down
	waiting   chan<- struct{}
	next      int
	tries     int
	left      time.Duration
	gen       int
	gaveUp    bool
	gaveUpMsg DepsFailedMsg
}

func NewReadyModel(items ...ReadyItem) ReadyModel {
	rows := make([]readyRow, len(items))
	now := time.Now()
	for i, it := range items {
		rows[i] = readyRow{ReadyItem: it, start: now}
	}
	return ReadyModel{rows: rows, sb: newSpinnerBar()}
}

// Busy is false: quitting while getting ready loses nothing.
func (m ReadyModel) Busy() bool { return false }

func (m ReadyModel) Init() tea.Cmd { return m.sb.spin.Tick }

func (m ReadyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.sb.bar.Width = clamp(m.w-50, 20, 40)
		return m, nil
	case InstallProgressMsg:
		if r := m.row(msg.Phase); r != nil {
			r.done, r.total, r.ready = msg.Done, msg.Total, msg.Ready
			r.failed = ""
			if !msg.Ready && msg.Total > 0 {
				return m, m.sb.bar.SetPercent(float64(msg.Done) / float64(msg.Total))
			}
		}
		return m, nil
	case RetryMsg:
		m.markFailed(msg.Failed)
		m.waiting, m.next, m.tries, m.left = msg.Go, msg.Next, msg.Tries, msg.Wait
		m.gen++
		return m, m.tick()
	case retryTickMsg:
		if msg.gen != m.gen || m.waiting == nil {
			return m, nil
		}
		m.left -= time.Second
		if m.left <= 0 {
			m.retryNow()
			return m, nil
		}
		return m, m.tick()
	case DepsFailedMsg:
		m.gaveUp, m.gaveUpMsg = true, msg
		m.markFailed(msg.Failed)
		return m, nil
	case tea.KeyMsg:
		switch {
		case m.gaveUp, msg.String() == "ctrl+c":
			return m, Left(Leave{Quit: true, Aborted: true})
		case msg.String() == "enter" && m.waiting != nil:
			m.retryNow()
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.sb, cmd = m.sb.update(msg)
	return m, cmd
}

func (m *ReadyModel) retryNow() {
	close(m.waiting)
	m.waiting = nil
	m.gen++
	for i := range m.rows {
		if m.rows[i].failed != "" {
			m.rows[i].failed = ""
			m.rows[i].start = time.Now()
		}
	}
}

func (m *ReadyModel) markFailed(failed map[string]string) {
	for phase, reason := range failed {
		if r := m.row(phase); r != nil {
			r.failed = reason
		}
	}
}

func (m ReadyModel) tick() tea.Cmd {
	gen := m.gen
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return retryTickMsg{gen: gen} })
}

func (m *ReadyModel) row(phase string) *readyRow {
	for i := range m.rows {
		if m.rows[i].Phase == phase {
			return &m.rows[i]
		}
	}
	return nil
}

func (m ReadyModel) View() string {
	var b strings.Builder
	b.WriteString(Brand() + "\n\n")
	if m.gaveUp {
		b.WriteString("  " + Bad.Render(fmt.Sprintf("✗ Couldn't download %s after %d tries", m.failedLabels(), m.gaveUpMsg.Tries)) + "\n\n")
	} else {
		b.WriteString("  " + Text.Bold(true).Render("Getting ready") + "\n\n")
	}
	for _, r := range m.rows {
		b.WriteString(m.rowView(r) + "\n")
	}
	b.WriteString("\n")
	var footer string
	switch {
	case m.gaveUp:
		b.WriteString("  " + DimText.Render("Check your connection and run wandersort again.") + "\n")
		b.WriteString("  " + DimText.Render("It picks up from what's already downloaded.") + "\n")
		footer = Footer(KeyHint("any key", "quit"), m.w)
	case m.waiting != nil:
		b.WriteString("  " + Attn.Render("⚠ Try switching to a better network.") + "\n")
		b.WriteString("    " + DimText.Render(fmt.Sprintf("Retrying in %s", m.left)) + "  " +
			FaintTxt.Render(fmt.Sprintf("try %d of %d", m.next, m.tries)) + "\n")
		footer = Footer(KeyHint("enter", "retry now")+"   "+KeyHint("ctrl+c", "quit"), m.w)
	default:
		footer = Footer(KeyHint("ctrl+c", "quit"), m.w)
	}
	return Screen(b.String(), footer, m.h)
}

func (m ReadyModel) rowView(r readyRow) string {
	name := fmt.Sprintf("%-13s", r.Label)
	switch {
	case r.failed != "":
		return row("  "+Bad.Render("✗")+" "+Text.Render(name)+" "+DimText.Render(r.failed), "", m.w)
	case r.ready && r.total > 0:
		return row("  "+OK.Render("✓")+" "+Text.Render(name)+" "+DimText.Render("downloaded, "+volume.HumanBytes(uint64(r.total))), "", m.w)
	case r.ready:
		return row("  "+OK.Render("✓")+" "+Text.Render(name)+" "+DimText.Render("found"), "", m.w)
	case r.total > 0:
		left := "  " + m.sb.spin.View() + Text.Render(name) + " " + m.sb.bar.View() + "  " +
			FaintTxt.Render(volume.HumanBytes(uint64(r.done))+" / "+volume.HumanBytes(uint64(r.total)))
		return row(left, FaintTxt.Render(liveElapsed(r.start)), m.w)
	default:
		return row("  "+m.sb.spin.View()+Text.Render(name)+" "+DimText.Render("checking…"), FaintTxt.Render(liveElapsed(r.start)), m.w)
	}
}

// failedLabels names the rows that failed: "exiftool and locationDB".
func (m ReadyModel) failedLabels() string {
	var names []string
	for _, r := range m.rows {
		if _, ok := m.gaveUpMsg.Failed[r.Phase]; ok {
			names = append(names, r.Label)
		}
	}
	if len(names) == 0 {
		return "what WanderSort needs"
	}
	return strings.Join(names, " and ")
}
