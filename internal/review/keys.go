// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/path"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height, m.width = msg.Height, msg.Width
		m.scrollIntoView()
		return m, nil
	case spinner.TickMsg:
		if !m.previewing && !m.rebuilding {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case SettingsChangedMsg:
		m.raiseRebuildAsk(true)
		return m, nil
	case rebuiltMsg:
		return m.rebuilt(msg), nil
	case previewDoneMsg:
		m.previewing = false
		m.previewErr = msg.err
		if msg.err == nil {
			openInViewer(msg.dir)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// answerRebuildAsk drives the rebuild modal: the same keys tui.ConfirmModel
// answers, since that is what it is drawn as. Anything else is swallowed —
// the question has to be answered, not scrolled past.
func (m Model) answerRebuildAsk(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "left":
		m.rebuildChoice = true
	case "right":
		m.rebuildChoice = false
	case "y":
		return m.startRebuild()
	case "n", "esc":
		m.askRebuild = false
	case "enter":
		if m.rebuildChoice {
			return m.startRebuild()
		}
		m.askRebuild = false
	}
	return m, nil
}

// answerExitAsk drives [esc]'s Save/Discard modal — the same shape as the
// config wizard's own exit ask, and the same keys tui.ConfirmModel answers
// with. A second [esc] here forcefully discards, no second-guessing needed:
// the modal itself was the warning. ctrl+c inside it is the harder "get me
// out" — it never backs to the picker, unlike Discard (see hardQuit).
func (m Model) answerExitAsk(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		m.askExit = false
		return m.hardQuit()
	case "left":
		m.exitChoice = true
	case "right":
		m.exitChoice = false
	case "y":
		m.askExit = false
		return m.saveAndExit()
	case "n":
		m.askExit = false
		return m.discardAndExit()
	case "enter":
		m.askExit = false
		if m.exitChoice {
			return m.saveAndExit()
		}
		return m.discardAndExit()
	case "esc":
		m.askExit = false
		return m.discardAndExit()
	}
	return m, nil
}

// saveAndExit is [esc]'s "Save" answer: the only way this screen writes
// anything, whether or not there was anything to edit in the first place —
// a reviewer approving the proposal exactly as offered still needs a key.
func (m Model) saveAndExit() (tea.Model, tea.Cmd) {
	m.confirmed, m.done = true, true
	if m.embedded {
		return m, nil
	}
	return m, tea.Quit
}

// discardAndExit is [esc]'s "Discard" answer: hosted, that's a step back to
// the time-slice picker (the other slices are still waiting); otherwise the
// review just ends with nothing written.
func (m Model) discardAndExit() (tea.Model, tea.Cmd) {
	m.done = true
	if m.hosted {
		m.back = true
	}
	if m.embedded {
		return m, nil
	}
	return m, tea.Quit
}

// hardQuit is ctrl+c's unconditional exit — never a step back to the picker,
// even hosted: "ctrl+c anywhere quits the app" is the one guarantee it makes,
// and honoring `hosted` here would break it.
func (m Model) hardQuit() (tea.Model, tea.Cmd) {
	m.done = true
	if m.embedded {
		return m, nil
	}
	return m, tea.Quit
}

// startRebuild runs the host's rebuild off the UI goroutine, behind the same
// spinner [p] uses. The modal was the confirmation, so nothing warns again.
func (m Model) startRebuild() (tea.Model, tea.Cmd) {
	m.askRebuild = false
	m.rebuilding = true
	m.statusMsg, m.statusIsErr = "", false
	rebuild, ctx, seg := m.rebuild, m.ctx, m.seg
	return m, tea.Batch(m.spin.Tick, func() tea.Msg {
		tree, err := rebuild(ctx, seg)
		return rebuiltMsg{tree: tree, err: err}
	})
}

func (m Model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		switch key.Type {
		case tea.KeyEnter:
			// an arrowed-onto suggestion: pick it into the input instead of
			// applying, same as the config wizard's completion list
			if m.suggCursor >= 0 && m.suggCursor < len(m.suggestions) {
				m.fillSuggestion(m.suggCursor)
				return m, nil
			}
			m.editing = false
			m.suggestions = nil
			m.applyRename(path.SanitizeSegment(m.input))
		case tea.KeyEsc:
			m.editing = false
			m.suggestions = nil
		case tea.KeyBackspace:
			if len(m.input) > 0 {
				r := []rune(m.input)
				m.input = string(r[:len(r)-1])
			}
			m.refreshSuggestions()
		case tea.KeySpace:
			m.input += " "
			m.refreshSuggestions()
		case tea.KeyRunes:
			m.input += string(key.Runes)
			m.refreshSuggestions()
		case tea.KeyUp:
			if m.suggCursor > -1 {
				m.suggCursor--
			}
		case tea.KeyDown:
			if m.suggCursor < len(m.suggestions)-1 {
				m.suggCursor++
			}
		case tea.KeyTab:
			if len(m.suggestions) > 0 {
				m.fillSuggestion(max(m.suggCursor, 0))
			}
		case tea.KeyCtrlE:
			m.radiusDelta += location.NearSearchDegrees
			m.loadGeoCandidates()
			m.refreshSuggestions()
		}
		return m, nil
	}

	// The rebuild question owns the screen until it is answered — that is the
	// point of it. ctrl+c still falls through, so the app is never trapped.
	if m.askRebuild && key.String() != "ctrl+c" {
		return m.answerRebuildAsk(key)
	}

	// Same shape for the exit question, except ctrl+c inside it is a hard
	// discard rather than a fall-through — the modal is already the warning,
	// so a second wait-and-warn cycle behind it would just trap the reviewer.
	if m.askExit {
		return m.answerExitAsk(key)
	}

	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	var cmd tea.Cmd
	m.quitWarned = m.quitWarned && key.String() == "ctrl+c"
	switch key.String() {
	case "ctrl+c":
		// ctrl+c never saves — it's the unconditional discard, warned once so
		// an accidental press doesn't throw work away.
		if m.hasEdits() && !m.quitWarned {
			m.quitWarned = true
			m.statusMsg, m.statusIsErr = "unsaved changes — press esc to save or discard them, or ctrl+c again to discard immediately", true
			break
		}
		return m.hardQuit()
	case "esc":
		// A live selection is the nearer thing to back out of — esc clears it
		// first, same as it does everywhere else.
		if m.visualMode {
			m.visualMode = false
			break
		}
		// Nothing edited, and a picker underneath: this was a look around, not
		// a decision. Asking "save or discard?" over a tree the reviewer never
		// touched is a question about nothing — [A] on the picker is how an
		// untouched slice gets approved. Unhosted there is no picker to go back
		// to, so the question is still the only way to approve.
		if m.hosted && !m.hasEdits() {
			return m.discardAndExit()
		}
		m.askExit, m.exitChoice = true, true
		return m, nil
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
	case "n":
		m.jumpSameDepth(1)
	case "N":
		m.jumpSameDepth(-1)
	case "r":
		m.input = m.rows[m.cursor].node.Name
		m.editing = true
		m.radiusDelta = location.NearSearchDegrees
		m.loadGeoCandidates()
		m.refreshSuggestions()
	case "p":
		if !m.previewing {
			m.previewing = true
			m.previewErr = nil
			cmd = tea.Batch(peekCmd(m.ctx, m.db, m.rows[m.cursor].node), m.spin.Tick)
		}
	case "V":
		if m.visualMode {
			m.visualMode = false
		} else {
			m.visualMode = true
			m.visualAnchor = m.cursor
		}
		m.statusMsg, m.statusIsErr = "", false
	case "m":
		m.mergeSelection()
	case "d":
		m.dropFolders(m.selectedRows())
	case "D":
		m.flattenFolders(m.selectedRows())
	case "?":
		m.showHelp = true
	case "R":
		if !m.rebuilding {
			m.raiseRebuildAsk(false) // the reviewer asked, nothing moved under them
		}
	case "u":
		if n := len(m.undo); n > 0 {
			step := m.undo[n-1]
			m.undo = m.undo[:n-1]
			m.tree = step.tree
			m.reflow()
			left := ""
			if len(m.undo) > 0 {
				left = fmt.Sprintf(" (%d more)", len(m.undo))
			}
			m.statusMsg, m.statusIsErr = "undid "+step.edit+left, false
		} else {
			m.statusMsg, m.statusIsErr = "nothing left to undo", true
		}
	}
	m.scrollIntoView()
	return m, cmd
}
