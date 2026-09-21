// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import tea "github.com/charmbracelet/bubbletea"

// Tab is a screen the app shell hosts. tea.Model is how it draws and handles
// keys; Busy is the one fact about a screen the container cannot work out for
// itself, and it needs nothing else.
//
// Before this there was no interface at all: the container knew each screen
// by its concrete type and reached past tea.Model in twenty-eight places, for
// five different method sets. Adding a screen meant adding another type
// assertion to every one of those places.
type Tab interface {
	tea.Model
	// Busy reports work in flight the container must not interrupt or replace
	// — a scan mid-run. A busy tab keeps its own claim on ctrl+c, is not
	// swapped out from under itself, and closes the tabs whose answers it is
	// about to invalidate.
	Busy() bool
}

// Leave is how a hosted screen hands control back. It is the *only* way: a
// screen must never return tea.Quit, because the container owns the program
// and a screen that ends it takes every other tab with it — including a scan
// still running underneath. What leaving means is the container's decision,
// not the screen's.
//
// This replaced nine separate hand-back paths: two screens calling tea.Quit
// directly (six sites), one reporting through a polled Done() the container
// had to check after every keystroke, one handing back a nil SwitchMsg, and a
// flag on the container to reinterpret the last two as a quit.
type Leave struct {
	// Quit asks for the session to end rather than a walk back to the home
	// tab: the user's key meant "I am done with the app", not "I am done
	// here". The container may still overrule it — a scan mid-run gets to ask
	// first.
	Quit bool
	// Aborted marks a screen left without its work being kept, so the
	// container knows not to act on an answer that was never given.
	Aborted bool
	// Err is why the screen ended, when it ended badly.
	Err error
	// Note is one line for the home screen: what was saved, what was kept.
	Note string
}

// Left is the command form of Leave, for a screen to return from Update.
func Left(l Leave) tea.Cmd { return func() tea.Msg { return l } }
