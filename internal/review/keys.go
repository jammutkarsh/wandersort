// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"fmt"
	"maps"

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
			m.previewDirs[msg.signature] = msg.dir
			openInViewer(msg.dir)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
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
	m.quitWarned = m.quitWarned && key.String() == "ctrl+c"
	switch key.String() {
	case "ctrl+c":
		if m.hasEdits() && !m.quitWarned {
			m.quitWarned = true
			m.statusMsg, m.statusIsErr = "unsaved changes — [c] saves and exits, press ctrl+c again to discard them", true
			break
		}
		m.done = true
		if m.embedded {
			return m, nil
		}
		return m, tea.Quit
	case "esc":
		m.visualMode = false
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
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
			node := m.rows[m.cursor].node
			cached := make(map[string]string, len(m.previewDirs))
			maps.Copy(cached, m.previewDirs)
			cmd = tea.Batch(peekCmd(m.ctx, m.db, node, cached), m.spin.Tick)
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
	case "c":
		m.confirmed = true
		m.done = true
		if m.embedded {
			return m, nil
		}
		return m, tea.Quit
	}
	m.scrollIntoView()
	return m, cmd
}
