// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package cli

import (
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/core/verify"
	"github.com/jammutkarsh/wandersort/pkg/db"
)

// Problems are listed under one heading per kind, in the order kinds first
// appear, each group's files in check order.
func TestGroupProblems(t *testing.T) {
	groups := groupProblems([]verify.Problem{
		{Path: "a.jpg", Kind: db.KindChecksumMismatch},
		{Path: "b.jpg", Kind: db.KindOther, Detail: "size is 1 B, the library recorded 2 B"},
		{Path: "c.jpg", Kind: db.KindChecksumMismatch},
	})
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want 2", groups)
	}
	if g := groups[0]; g.heading != "files changed since they were copied in" || g.detail ||
		len(g.problems) != 2 || g.problems[0].Path != "a.jpg" || g.problems[1].Path != "c.jpg" {
		t.Errorf("first group = %+v, want the two changed files, no per-file detail", g)
	}
	if g := groups[1]; !g.detail || len(g.problems) != 1 || g.problems[0].Path != "b.jpg" {
		t.Errorf("second group = %+v, want b.jpg with its detail", g)
	}
}

// Files that are gone are forgotten, which leaves the records true: the check
// lists them but does not fail.
func TestReportVerifyForgottenOnlyIsNotAFailure(t *testing.T) {
	rep := verify.Report{Checked: 3, Database: "ok", Forgotten: []string{"orphan/a.AAE"}}
	if err := reportVerify(rep, false); err != nil {
		t.Errorf("reportVerify = %v, want nil", err)
	}
	rep.Problems = []verify.Problem{{Path: "b.jpg", Kind: db.KindOther}}
	if err := reportVerify(rep, false); err == nil {
		t.Error("reportVerify = nil with a damaged file, want an error")
	}
}
