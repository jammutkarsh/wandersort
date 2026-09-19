// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// TestSettingsChanged pins the three answers a re-plan hangs on: no stamp is
// never a change (a proposal from before stamping must not re-plan), a
// matching stamp is not a change, and a settings edit is.
func TestSettingsChanged(t *testing.T) {
	cfg := testConfig(t)
	a := &app{Config: cfg, Log: logger.NewNoopLogger()}
	outputDir := t.TempDir()

	if a.settingsChanged(outputDir) {
		t.Error("no stamp file must never read as a settings change")
	}

	if err := vfs.WriteStamp(outputDir, vfs.ConfigStamp(vfs.ConfigFor(cfg))); err != nil {
		t.Fatal(err)
	}
	if a.settingsChanged(outputDir) {
		t.Error("the stamp the current settings produce must not read as a change")
	}

	a.Config.Rules = []string{vfs.RuleDevice, vfs.RuleMedia}
	if !a.settingsChanged(outputDir) {
		t.Error("changed rules must read as a settings change")
	}
}
