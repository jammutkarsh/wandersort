//go:build !windows

package vfs

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

// After a journal write that landed but could not be made durable, the draft
// is what the journal holds: what a copy would apply. A folder that can be
// written but not read fails only the sync that follows the write.
func TestDraftMatchesTheJournalAfterAFailedSync(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a folder whatever its permissions")
	}
	dir := t.TempDir()
	d, err := OpenDraft(dir, draftTree())
	if err != nil {
		t.Fatal(err)
	}
	failSyncs := func() {
		t.Helper()
		if err := os.Chmod(dir, 0o300); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	sameAsJournal := func(what string, want int) {
		t.Helper()
		onDisk, err := CountEdits(dir)
		if err != nil {
			t.Fatal(err)
		}
		if onDisk != want || len(d.Edits()) != want {
			t.Errorf("after %s the draft holds %d edits and the journal %d, want %d", what, len(d.Edits()), onDisk, want)
		}
	}

	failSyncs()
	if _, err := d.Apply(Edit{Op: OpRename, Node: 3, To: "One"}); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Apply = %v, want the failed sync", err)
	}
	sameAsJournal("a failed Apply", 1)
	if n := FindNode(d.Tree(), 3); n.Name != "One" {
		t.Errorf("folder = %q, want the name the copy would give it", n.Name)
	}

	os.Chmod(dir, 0o700)
	if _, err := d.Apply(Edit{Op: OpRename, Node: 4, To: "Two"}); err != nil {
		t.Fatal(err)
	}
	failSyncs()
	if _, err := d.Undo(); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Undo = %v, want the failed sync", err)
	}
	sameAsJournal("a failed Undo", 1)
	if n := FindNode(d.Tree(), 4); n.Name != "04" {
		t.Errorf("folder = %q, want the undone name", n.Name)
	}
}
