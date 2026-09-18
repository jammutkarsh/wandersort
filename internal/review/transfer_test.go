// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jammutkarsh/wandersort/pkg/core/execute"
	"github.com/jammutkarsh/wandersort/pkg/core/vfs"
	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// approvedModel is a Model over one APPROVED entry with no real file on disk
// — enough to drive execute.Run and see it fail at the source-stat step,
// which is exactly what proves a key really dispatched a transfer rather than
// being a no-op.
func approvedModel(t *testing.T) (Model, *db.DB) {
	t.Helper()
	d := dbtest.New(t)
	insertVFSEntry(t, d, 1, "/src/a.jpg", "2024/06_June/a.jpg")
	if _, err := d.ExecContext(context.Background(),
		`UPDATE virtual_fs_entries SET status = 'APPROVED'`); err != nil {
		t.Fatal(err)
	}
	m := newModel(nil, context.Background(), d, nil, logger.NewNoopLogger(), t.TempDir())
	m.width, m.height = 100, 24
	return m, d
}

func drainTransferred(t *testing.T, msg tea.Msg) transferredMsg {
	t.Helper()
	switch m := msg.(type) {
	case transferredMsg:
		return m
	case tea.BatchMsg:
		for _, c := range m {
			if c == nil {
				continue
			}
			if tm, ok := c().(transferredMsg); ok {
				return tm
			}
		}
	}
	t.Fatalf("got %T, want transferredMsg", msg)
	return transferredMsg{}
}

// TestCopyKeyRunsTransferWithoutAsking: [x] never touches a source, so it
// runs immediately — no modal in the way, unlike [X].
func TestCopyKeyRunsTransferWithoutAsking(t *testing.T) {
	m, _ := approvedModel(t)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got := next.(Model)
	if !got.transferring || got.askMove {
		t.Fatalf("[x] should start transferring immediately, got transferring=%v askMove=%v", got.transferring, got.askMove)
	}
	if cmd == nil {
		t.Fatal("[x] dispatched no command")
	}

	msg := drainTransferred(t, cmd())
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if msg.rep.Failed == 0 {
		t.Fatal("expected the transfer to report a failure for the fixture's fake source path")
	}

	done, syncCmd := got.transferred(msg)
	if done.transferring {
		t.Error("still transferring after the result landed")
	}
	if !done.statusIsErr || done.statusMsg == "" {
		t.Error("a failed transfer did not report a status")
	}
	if syncCmd == nil {
		t.Fatal("transferred did not dispatch a tree reload")
	}
	if rm, ok := syncCmd().(resetMsg); !ok || rm.err != nil {
		t.Fatalf("expected a clean resetMsg reload, got %#v", syncCmd())
	}
	// The reload must not overwrite the status the transfer itself reported.
	synced := done.reset(resetMsg{tree: nil})
	if !synced.statusIsErr || synced.statusMsg != done.statusMsg {
		t.Errorf("post-transfer reset changed the status: got %q, want %q", synced.statusMsg, done.statusMsg)
	}
}

// TestMoveKeyAsksFirst: [X] deletes files, so unlike [x] it raises a
// question, defaults to Cancel, and only starts on an explicit yes.
func TestMoveKeyAsksFirst(t *testing.T) {
	m, _ := approvedModel(t)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	got := next.(Model)
	if !got.askMove || got.moveChoice {
		t.Fatalf("[X] should ask with Cancel as the default, got askMove=%v moveChoice=%v", got.askMove, got.moveChoice)
	}
	if cmd != nil {
		t.Fatal("[X] should not start anything before the question is answered")
	}

	next, cmd = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got = next.(Model)
	if got.askMove {
		t.Error("[n] should dismiss the ask")
	}
	if got.transferring || cmd != nil {
		t.Fatal("[n] must not start a transfer")
	}

	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	next, cmd = next.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got = next.(Model)
	if got.askMove || !got.transferring {
		t.Fatal("[y] must start the move on that press")
	}
	if cmd == nil {
		t.Fatal("[y] dispatched no command")
	}
	msg := drainTransferred(t, cmd())
	if msg.mode != execute.ModeMove {
		t.Errorf("mode = %v, want ModeMove", msg.mode)
	}
}

// TestTransferRefusesWithNothingApproved: [x]/[X] over an empty plan report a
// status instead of dispatching execute.Run.
func TestTransferRefusesWithNothingApproved(t *testing.T) {
	m, _ := newDBModelNoEntries(t)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got := next.(Model)
	if got.transferring || cmd != nil {
		t.Fatal("[x] must not start a transfer with nothing approved")
	}
	if !got.statusIsErr || got.statusMsg == "" {
		t.Error("expected a status explaining there is nothing to transfer")
	}
}

func newDBModelNoEntries(t *testing.T) (Model, *db.DB) {
	t.Helper()
	d := dbtest.New(t)
	return newModel(nil, context.Background(), d, nil, logger.NewNoopLogger(), t.TempDir()), d
}

// TestCopyKeyApprovesTheFreshProposalFirst: on a review nobody has saved yet
// (every row still PROPOSED), [x] used to refuse with "nothing approved yet"
// — the only way to transfer was esc → Save, closing the review, then
// reopening it to press [x] again. [x] now approves the plan on screen before
// checking what there is to transfer, so it works on the very first press.
func TestCopyKeyApprovesTheFreshProposalFirst(t *testing.T) {
	d := dbtest.New(t)
	insertVFSEntry(t, d, 1, "/src/a.jpg", "2024/06_June/a.jpg") // status: PROPOSED
	ctx := context.Background()
	tree, err := vfs.BuildTree(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(tree, ctx, d, nil, logger.NewNoopLogger(), t.TempDir())
	m.width, m.height = 100, 24

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got := next.(Model)
	if got.statusIsErr {
		t.Fatalf("[x] refused a fresh proposal: %q", got.statusMsg)
	}
	if !got.transferring || cmd == nil {
		t.Fatal("[x] should approve the plan and start transferring on the first press")
	}
	var status string
	if err := d.SQL.GetContext(ctx, &status, `SELECT status FROM virtual_fs_entries WHERE file_id = 1`); err != nil {
		t.Fatal(err)
	}
	if status != db.StatusApproved {
		t.Errorf("status = %q, want %q — [x] must approve before it transfers", status, db.StatusApproved)
	}
}

// TestCopyKeyTransfersTheRenameOnScreen covers the worse case: rename "Goa" to
// "Goa Trip" on screen, then press [x] without ever pressing esc → Save. The
// old behaviour transferred files under the un-renamed "Goa" — files a
// transfer marks DONE can never be re-planned, so the name on screen has to be
// the name that lands on disk.
func TestCopyKeyTransfersTheRenameOnScreen(t *testing.T) {
	d := dbtest.New(t)
	insertVFSEntry(t, d, 1, "/src/a.jpg", "2024/06_June/Goa/a.jpg")
	ctx := context.Background()
	tree, err := vfs.BuildTree(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(tree, ctx, d, nil, logger.NewNoopLogger(), t.TempDir())
	m.width, m.height = 100, 24
	for i, r := range m.rows {
		if r.node.Name == "Goa" {
			m.cursor = i
		}
	}
	m.applyRename("Goa Trip")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got := next.(Model)
	if got.statusIsErr || cmd == nil {
		t.Fatalf("[x] with a pending rename should still transfer, got status %q", got.statusMsg)
	}
	var targetPath string
	if err := d.SQL.GetContext(ctx, &targetPath, `SELECT target_path FROM virtual_fs_entries WHERE file_id = 1`); err != nil {
		t.Fatal(err)
	}
	if targetPath != "2024/06_June/Goa-Trip/a.jpg" {
		t.Errorf("target_path = %q, want the renamed folder — [x] transferred under the stale name", targetPath)
	}
}

// TestEscAndResetAreBlockedWhileTransferring: a running transfer already
// approved and is writing the plan as it stood the moment it started. [esc]
// re-saving over it, or [R] reloading out from under it, would race that
// write — both must wait; ctrl+c is unaffected.
func TestEscAndResetAreBlockedWhileTransferring(t *testing.T) {
	m, _ := approvedModel(t)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(Model)
	if !m.transferring || cmd == nil {
		t.Fatal("setup: [x] should have started a transfer")
	}

	next, escCmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	afterEsc := next.(Model)
	if afterEsc.askExit {
		t.Error("[esc] must not raise the save/discard ask while a transfer is running")
	}
	if escCmd != nil {
		t.Error("[esc] must not dispatch anything while a transfer is running")
	}
	if !afterEsc.statusIsErr || afterEsc.statusMsg == "" {
		t.Error("[esc] during a transfer should explain why nothing happened")
	}

	next, resetCmd := afterEsc.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	afterReset := next.(Model)
	if afterReset.resetting {
		t.Error("[R] must not start reloading while a transfer is running")
	}
	if resetCmd != nil {
		t.Error("[R] must not dispatch anything while a transfer is running")
	}
}

// TestFreeSpaceRefusalLeavesThePlanUnsaved is the regression for the stuck
// review: a refusal used to land *after* startTransfer's Confirm already
// saved the plan, so the in-memory tree (with the reviewer's rename) and the
// database (still the old name) disagreed, and every later save failed with
// "invalid review tree: unknown node id". The free-space check now runs
// before Confirm, so a refusal changes nothing on disk — rename, get
// refused, then save must still work.
func TestFreeSpaceRefusalLeavesThePlanUnsaved(t *testing.T) {
	d := dbtest.New(t)
	insertVFSEntry(t, d, 1, "/src/a.jpg", "2024/06_June/Goa/a.jpg")
	// Bigger than any real disk's free space, so the check refuses
	// deterministically without mocking volume.FreeBytes.
	if _, err := d.ExecContext(context.Background(),
		`UPDATE file_registry SET file_size = ?`, int64(1)<<62); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tree, err := vfs.BuildTree(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(tree, ctx, d, nil, logger.NewNoopLogger(), t.TempDir())
	m.width, m.height = 100, 24
	for i, r := range m.rows {
		if r.node.Name == "Goa" {
			m.cursor = i
		}
	}
	m.applyRename("Goa Trip")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got := next.(Model)
	if !got.statusIsErr || cmd != nil || got.transferring {
		t.Fatalf("expected [x] to refuse for lack of free space, got status=%q transferring=%v cmd=%v",
			got.statusMsg, got.transferring, cmd)
	}

	var status string
	if err := d.SQL.GetContext(ctx, &status, `SELECT status FROM virtual_fs_entries WHERE file_id = 1`); err != nil {
		t.Fatal(err)
	}
	if status != db.StatusProposed {
		t.Fatalf("status = %q, want %q — a refusal must not save the plan", status, db.StatusProposed)
	}

	if err := vfs.Confirm(ctx, d, got.tree); err != nil {
		t.Fatalf("save after a refused transfer failed: %v", err)
	}
}

// TestTransferFailureReloadsTheTree is the other half of the same stuck
// review: the transfer itself failing outright (its database backup, here)
// used to leave the pre-save tree on screen even though Confirm had already
// committed the rename — reload on the error path fixes it.
func TestTransferFailureReloadsTheTree(t *testing.T) {
	d := dbtest.New(t)
	insertVFSEntry(t, d, 1, "/src/a.jpg", "2024/06_June/Goa/a.jpg")
	ctx := context.Background()
	tree, err := vfs.BuildTree(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	// A plain file where execute.Run expects an output directory: its
	// database backup ("VACUUM INTO <outputDir>/.wandersort.db.bak.tmp")
	// fails outright, before a single file transfers.
	outputDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(outputDir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel(tree, ctx, d, nil, logger.NewNoopLogger(), outputDir)
	m.width, m.height = 100, 24
	for i, r := range m.rows {
		if r.node.Name == "Goa" {
			m.cursor = i
		}
	}
	m.applyRename("Goa Trip")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got := next.(Model)
	if !got.transferring || cmd == nil {
		t.Fatal("setup: [x] should have approved the rename and started the transfer")
	}
	msg := drainTransferred(t, cmd())
	if msg.err == nil {
		t.Fatal("expected execute.Run to fail against a non-directory output path")
	}

	done, syncCmd := got.transferred(msg)
	if !done.statusIsErr {
		t.Error("a failed transfer must report the error")
	}
	if syncCmd == nil {
		t.Fatal("a failed transfer must still reload the tree")
	}
	rm, ok := syncCmd().(resetMsg)
	if !ok || rm.err != nil {
		t.Fatalf("expected a clean resetMsg reload, got %#v", rm)
	}
	synced := done.reset(rm)
	if len(synced.tree) == 0 {
		t.Fatal("reload left nothing on screen")
	}

	// The later "esc → Save" this used to break must still work.
	if err := vfs.Confirm(ctx, d, synced.tree); err != nil {
		t.Fatalf("save after a failed transfer failed: %v", err)
	}
	var targetPath string
	if err := d.SQL.GetContext(ctx, &targetPath, `SELECT target_path FROM virtual_fs_entries WHERE file_id = 1`); err != nil {
		t.Fatal(err)
	}
	if targetPath != "2024/06_June/Goa-Trip/a.jpg" {
		t.Errorf("target_path = %q, the rename Confirm already saved should have survived the reload", targetPath)
	}
}

// TestTransferEverythingClosesTheReviewCleanly is the low-severity regression:
// copying (or moving) the last reviewable row left the pre-transfer tree on
// screen, since the reload found nothing left and, for a manual [R], kept the
// old tree on purpose. After a transfer that is the wrong call — the
// reviewer's next [esc] -> Save then hit Confirm with zero reviewable rows
// and failed with a false "proposal was replaced by a newer scan". With
// nothing left to review, the screen now closes instead, and closes clean.
func TestTransferEverythingClosesTheReviewCleanly(t *testing.T) {
	d := dbtest.New(t)
	src := filepath.Join(t.TempDir(), "a.jpg")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	insertVFSEntry(t, d, 1, src, "2024/06_June/a.jpg") // status: PROPOSED
	ctx := context.Background()
	tree, err := vfs.BuildTree(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(tree, ctx, d, nil, logger.NewNoopLogger(), t.TempDir())
	m.width, m.height = 100, 24

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got := next.(Model)
	if !got.transferring || cmd == nil {
		t.Fatal("setup: [x] should have approved the plan and started the copy")
	}
	msg := drainTransferred(t, cmd())
	if msg.err != nil || msg.rep.Failed > 0 {
		t.Fatalf("expected a clean copy, got err=%v rep=%+v", msg.err, msg.rep)
	}

	done, syncCmd := got.transferred(msg)
	if done.statusIsErr {
		t.Fatalf("a successful transfer must not report an error: %q", done.statusMsg)
	}
	rm, ok := syncCmd().(resetMsg)
	if !ok || rm.err != nil {
		t.Fatalf("expected a clean resetMsg reload, got %#v", rm)
	}
	if len(rm.tree) != 0 {
		t.Fatalf("expected nothing left to review after copying the only row, got %+v", rm.tree)
	}

	final := done.reset(rm)
	if final.statusIsErr {
		t.Errorf("closing after a full transfer must not surface an error, got %q", final.statusMsg)
	}
	if !final.done {
		t.Error("with nothing left to review, the screen should close instead of keeping the pre-transfer tree")
	}
}

// TestScreenReportsTransferAfterAutoClose is the end-to-end regression: a
// transfer that empties the tree closes the review on its own (reset's
// postTransferSync path), and Outcome used to report Confirmed=false with no
// hint a transfer ever ran — reportReviewOutcome then said "Review cancelled
// — nothing changed" right after a clean copy. Outcome must carry what the
// transfer did regardless of how the screen ended up closing.
func TestScreenReportsTransferAfterAutoClose(t *testing.T) {
	d := dbtest.New(t)
	src := filepath.Join(t.TempDir(), "a.jpg")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	insertVFSEntry(t, d, 1, src, "2024/06_June/a.jpg")
	ctx := context.Background()
	tree, err := vfs.BuildTree(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	s := Screen(ctx, Options{DB: d, Tree: tree, Log: logger.NewNoopLogger(), OutputDir: t.TempDir()})

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	sm := next.(screen)
	if cmd == nil {
		t.Fatal("setup: [x] should have started the transfer")
	}
	msg := drainTransferred(t, cmd())
	if msg.err != nil || msg.rep.Failed > 0 {
		t.Fatalf("expected a clean copy, got err=%v rep=%+v", msg.err, msg.rep)
	}

	next, cmd = sm.Update(msg)
	sm = next.(screen)
	if cmd == nil {
		t.Fatal("transferred should have dispatched the post-copy tree reload")
	}
	rm, ok := cmd().(resetMsg)
	if !ok || rm.err != nil || len(rm.tree) != 0 {
		t.Fatalf("expected a clean, empty resetMsg reload, got %#v", rm)
	}

	next, switchCmd := sm.Update(rm)
	sm = next.(screen)
	if !sm.inner.done {
		t.Fatal("emptying the tree after a transfer should have closed the review")
	}
	if switchCmd == nil {
		t.Fatal("expected the tui.Switch(nil) that hands control back to the shell")
	}

	res, ok := Outcome(sm)
	if !ok {
		t.Fatal("Outcome should recognize a screen")
	}
	if res.Confirmed {
		t.Error("the review was never explicitly saved — Confirmed must be false")
	}
	if res.TransferDone != 1 || res.TransferFailed != 0 {
		t.Errorf("TransferDone/Failed = %d/%d, want 1/0", res.TransferDone, res.TransferFailed)
	}
}
