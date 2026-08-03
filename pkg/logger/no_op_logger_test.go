// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import "testing"

// TestNoopLoggerDoesNotPanic pins the whole point of the no-op logger: every
// method is safe to call with any args and does nothing observable.
func TestNoopLoggerDoesNotPanic(t *testing.T) {
	l := NewNoopLogger()
	l.Debug("x", "k", "v")
	l.Info("x")
	l.Warn("x", "k", 1, "k2", 2)
	l.Error("x")
}
