// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jammutkarsh/wandersort/pkg/path"
)

// StartScanMsg asks the shell to scan Paths — enter on an empty input with at
// least one folder collected. Force re-reads every already-scanned file from
// disk instead of skipping unchanged ones (ctrl+g, confirmed).
type StartScanMsg struct {
	Paths []string
	Force bool
}

// OpenReviewMsg asks the shell to review the proposal already in the database.
type OpenReviewMsg struct{}

// HomeErrMsg hands the home screen something that failed on the shell's side
// (an output directory locked by another process, an empty proposal) to render
// above its footer. The app stays alive either way — nothing here is fatal.
type HomeErrMsg struct{ Err error }

// HomeConfig wires the home screen to the shell.
type HomeConfig struct {
	// Suggest completes a typed path; nil disables the dropdown.
	Suggest func(typed string) []string
	// LastScan is the finished scan's summary, rendered dim above the input —
	// empty on the first run, filled once a scan in this session has completed.
	LastScan []string
}

// maxHomeSuggestions caps the completion list under the input; it renders above
// the footer, so an unbounded list would push the folder list off screen.
const maxHomeSuggestions = 5

// HomeModel is the app's landing screen: a folder list built one path per
// enter, with shell-style directory completion. One path per enter is what
// keeps folders with spaces in them working — no quoting, no comma escaping.
type HomeModel struct {
	cfg   HomeConfig
	ti    textinput.Model
	paths *path.Resolver
	// added holds folders expanded — the scan needs real paths — and they are
	// rendered back home-relative, the way they were typed and the way every
	// completion offers them.
	added      []string
	sugg       []string
	suggCursor int // ↑/↓-picked completion; -1 = none picked
	err        error
	w, h       int

	// confirmForce is a full-screen y/n asking before a force re-scan — the
	// modal owns the keyboard except ctrl+c, same as the review package's
	// rebuild ask.
	confirmForce bool
}

func NewHomeModel(cfg HomeConfig) HomeModel {
	paths := path.New()
	ti := textinput.New()
	ti.Prompt = lipgloss.NewStyle().Foreground(Primary).Render("» ")
	// Home-relative and OS-separated, not a hardcoded unix path: Windows'
	// equivalent lives under its own user profile drive, never "~/Pictures".
	ti.Placeholder = paths.RelativeToHome(filepath.Join(paths.HomeDir, "Pictures"))
	ti.Focus()
	m := HomeModel{cfg: cfg, ti: ti, paths: paths, suggCursor: -1}
	m.refresh()
	return m
}

// Paths is the folder list collected so far — the caller reads it back after
// the program exits.
func (m HomeModel) Paths() []string { return m.added }

func (m HomeModel) Init() tea.Cmd { return textinput.Blink }

func (m HomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case HomeErrMsg:
		m.err = msg.Err
		return m, nil
	case tea.KeyMsg:
		if m.confirmForce {
			// Only esc/enter here — no y/n, no arrows: a decision this
			// consequential (re-reading every file) gets exactly two keys.
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "enter":
				m.confirmForce = false
				paths := slices.Clone(m.added)
				return m, func() tea.Msg { return StartScanMsg{Paths: paths, Force: true} }
			case "esc":
				m.confirmForce = false
			}
			return m, nil
		}
		// Every letter is ordinary input here — the input is always focused —
		// so the screen's own commands are all ctrl-chorded.
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+g":
			if len(m.added) > 0 {
				m.confirmForce = true
			}
			return m, nil
		case "ctrl+x":
			m.drop(len(m.added) - 1)
			return m, nil
		case "up":
			switch {
			case m.suggCursor > -1: // walking the completion list
				m.suggCursor--
			case len(m.sugg) == 0 && len(m.added) > 0:
				// Only once the completions are out of the way: ↑ is the
				// dropdown's key first. Straight into editing the folder just
				// added — no separate select-then-enter step.
				return m, m.editLast()
			}
			return m, nil
		case "down":
			if m.suggCursor < len(m.sugg)-1 {
				m.suggCursor++
			}
			return m, nil
		case "tab":
			if len(m.sugg) > 0 {
				m.fill(max(m.suggCursor, 0))
			}
			return m, nil
		case "enter":
			// an arrowed-onto completion: pick it instead of adding the folder
			if m.suggCursor >= 0 && m.suggCursor < len(m.sugg) {
				m.fill(m.suggCursor)
				return m, nil
			}
			return m.enter()
		}
	}

	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	if _, isKey := msg.(tea.KeyMsg); isKey {
		m.refresh()
	}
	return m, cmd
}

// editLast pulls the most recently added folder back into the input to
// correct it, rather than making the user retype a whole path to fix one
// character.
func (m *HomeModel) editLast() tea.Cmd {
	i := len(m.added) - 1
	m.ti.SetValue(m.paths.RelativeToHome(m.added[i]))
	m.ti.CursorEnd()
	m.drop(i)
	m.refresh()
	return m.ti.Focus()
}

// drop removes one folder.
func (m *HomeModel) drop(i int) {
	if i < 0 || i >= len(m.added) {
		return
	}
	m.added = slices.Delete(m.added, i, i+1)
}

// enter adds the typed folder to the list, or starts the scan when the input
// is empty.
func (m HomeModel) enter() (tea.Model, tea.Cmd) {
	typed := strings.TrimSpace(m.ti.Value())
	if typed == "" {
		if len(m.added) == 0 {
			m.err = fmt.Errorf("type a folder to scan first")
			return m, nil
		}
		paths := slices.Clone(m.added)
		return m, func() tea.Msg { return StartScanMsg{Paths: paths} }
	}

	dir := m.paths.ExpandPath(typed)
	st, err := os.Stat(dir)
	switch {
	case err != nil:
		m.err = fmt.Errorf("%s — no such folder", typed)
		return m, nil
	case !st.IsDir():
		m.err = fmt.Errorf("%s is not a folder", typed)
		return m, nil
	}
	m.err = nil
	if !slices.Contains(m.added, dir) {
		m.added = append(m.added, dir)
	}
	m.ti.SetValue("")
	m.refresh()
	return m, nil
}

// fill writes the picked completion into the input. Suggestions come from the
// local filesystem, so they're refreshed synchronously — no debounce needed.
// Every suggestion is a directory (suggestDirs filters to dirs only), so a
// trailing "/" always applies — it's what tells the reader tab landed inside
// a real folder, and refresh() immediately lists what's below it.
func (m *HomeModel) fill(i int) {
	m.ti.SetValue(m.sugg[i] + "/")
	m.ti.CursorEnd()
	m.refresh()
}

func (m *HomeModel) refresh() {
	m.suggCursor = -1
	m.sugg = nil
	if m.cfg.Suggest != nil {
		m.sugg = m.cfg.Suggest(m.ti.Value())
	}
}

func (m HomeModel) View() string {
	if m.confirmForce {
		// "yes" is the default highlight — only its own key handling above
		// drives this (enter/esc), not ConfirmModel's own Update; see the
		// review package's rebuild ask for the same built-per-frame pattern.
		yes := true
		cm := NewConfirmModel("Force re-scan?",
			"Re-reads every already-scanned file from disk instead of skipping "+
				"unchanged ones. Slower — use after upgrading WanderSort, or if "+
				"a file's metadata looks wrong.", &yes)
		cm.Keys = fmt.Sprintf("%s / %s", KeyHint("enter", "confirm"), KeyHint("esc", "cancel"))
		cm.w, cm.h = m.w, m.h
		return cm.View()
	}

	var b strings.Builder
	b.WriteString(Banner("scan"))
	b.WriteString("\n")

	for _, l := range m.cfg.LastScan {
		b.WriteString(row(FaintTxt.Render(" # ")+DimText.Render(l), "", m.w))
		b.WriteString("\n")
	}

	title := "Folders to scan"
	if len(m.cfg.LastScan) > 0 {
		title = "Add more folders to scan"
	}
	b.WriteString(row(" "+Title.Render(title), "", m.w))
	b.WriteString("\n")
	for i, p := range m.added {
		line := "  " + OK.Render(fmt.Sprintf("%d) ", i+1)) + Text.Render(m.paths.RelativeToHome(p))
		b.WriteString(row(" "+line, "", m.w))
		b.WriteString("\n")
	}
	b.WriteString("\n   ")
	b.WriteString(m.ti.View())
	b.WriteString("\n")

	start, end := suggWindow(len(m.sugg), m.suggCursor, maxHomeSuggestions)
	if start > 0 {
		b.WriteString(row("      "+FaintTxt.Render(fmt.Sprintf("↑ %d more", start)), "", m.w) + "\n")
	}
	for i := start; i < end; i++ {
		var line string
		switch {
		case i == m.suggCursor:
			line = "      " + Selected.Render(m.sugg[i]) + FaintTxt.Render("  ⏎ pick")
		case i == 0 && m.suggCursor < 0:
			line = "      " + FaintTxt.Render("· ") + DimText.Render(m.sugg[i]) + FaintTxt.Render("  ⇥ tab")
		default:
			line = "      " + FaintTxt.Render("· ") + DimText.Render(m.sugg[i])
		}
		b.WriteString(row(line, "", m.w) + "\n")
	}
	if rest := len(m.sugg) - end; rest > 0 {
		b.WriteString(row("      "+FaintTxt.Render(fmt.Sprintf("↓ %d more", rest)), "", m.w) + "\n")
	}

	return Screen(b.String(), m.footer(), m.h)
}

func (m HomeModel) footer() string {
	var b strings.Builder
	if m.err != nil {
		b.WriteString(row(Attn.Render("⚠ "+m.err.Error()), "", m.w))
		b.WriteString("\n")
	}
	var hints []string
	if len(m.sugg) > 0 {
		hints = append(hints, KeyHint("↑↓", "pick"), KeyHint("tab", "complete"))
	}
	hints = append(hints, KeyHint("enter", "add folder"))
	if len(m.added) > 0 {
		hints = append(hints, KeyHint("enter on empty", "start scan"))
		if len(m.sugg) == 0 {
			hints = append(hints, KeyHint("↑", "edit a folder"))
		}
		hints = append(hints, KeyHint("ctrl+x", "remove last"))
		hints = append(hints, KeyHint("ctrl+g", "force re-scan"))
	}
	hints = append(hints, KeyHint("ctrl+c", "quit"))
	b.WriteString(Footer(strings.Join(hints, "   "), m.w))
	return b.String()
}
