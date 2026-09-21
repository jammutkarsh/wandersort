// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/location"
	"github.com/jammutkarsh/wandersort/pkg/path"
	"github.com/jammutkarsh/wandersort/pkg/tui"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height, m.width = msg.Height, msg.Width
		m.scrollIntoView()
		return m, nil
	case spinner.TickMsg:
		if !m.previewing {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
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

// leave hands back to the shell. Nothing to save or discard: every edit is
// already in the draft file, and stays there for the next review or for
// `wandersort execute` to apply. quit says whether the key meant "done with
// the app" or just "done here" — the shell decides what that costs.
func (m Model) leave(quit bool) (tea.Model, tea.Cmd) {
	return m, tui.Left(tui.Leave{Quit: quit})
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

	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	var cmd tea.Cmd
	switch key.String() {
	case "ctrl+c":
		return m.leave(true)
	case "esc":
		// A live selection is the nearer thing to back out of — esc clears it
		// first, same as it does everywhere else.
		if m.visualMode {
			m.visualMode = false
			break
		}
		return m.leave(false)
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
		if m.rows[m.cursor].node.Fixed() {
			m.statusMsg, m.statusIsErr = vfs.ErrFixedFolder.Error(), true
			break
		}
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
		m = m.reset()
	case "u":
		m.undo()
	}
	m.scrollIntoView()
	return m, cmd
}
