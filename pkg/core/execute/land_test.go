package execute

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
)

// Landing is tested through the transfer seam alone: temp folders, no
// database. commit records what it was asked to record.

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func recorder(committed *[]string) commitFn {
	return func(landed string) error {
		*committed = append(*committed, landed)
		return nil
	}
}

// A gone source whose file is already in the library, past a gap in the _N
// names, is recorded where it sits, by the real transfer and the dry run
// alike.
func TestTransferRecoversLandedFileWhenSourceIsGone(t *testing.T) {
	for name, xfer := range map[string]transfer{"real": productionTransfer, "dry run": dryRunTransfer} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dst := filepath.Join(dir, "lib", "A.jpg")
			writeFile(t, dst, "someone else's photo")
			writeFile(t, withSuffix(dst, 2), "hello")

			var committed []string
			landed, _, err := xfer(context.Background(), ModeMove, filepath.Join(dir, "gone.jpg"), dst,
				scanned{hash: hashOf("hello"), size: 5}, recorder(&committed))
			if err != nil {
				t.Fatal(err)
			}
			if want := withSuffix(dst, 2); landed != want || len(committed) != 1 || committed[0] != want {
				t.Errorf("landed %s, committed %v; want %s", landed, committed, want)
			}
		})
	}
}

// A gone source with nothing in the library is a stat failure, and nothing is
// recorded.
func TestTransferFailsWhenSourceIsGoneAndNothingLanded(t *testing.T) {
	dir := t.TempDir()
	var committed []string
	_, _, err := productionTransfer(context.Background(), ModeCopy, filepath.Join(dir, "gone.jpg"),
		filepath.Join(dir, "lib", "A.jpg"), scanned{hash: hashOf("hello"), size: 5}, recorder(&committed))
	if failedOp(err) != opStat {
		t.Errorf("err = %v (op %s), want a stat failure", err, failedOp(err))
	}
	if len(committed) != 0 {
		t.Errorf("committed %v for a file that never landed", committed)
	}
}

// A move whose source changed since the scan goes through the hash-checked
// copy, which refuses it: nothing lands, nothing is recorded, source kept.
func TestTransferMoveRefusesSourceChangedSinceScan(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "A.jpg")
	writeFile(t, src, "HELLO")
	dst := filepath.Join(dir, "lib", "A.jpg")

	var committed []string
	_, _, err := productionTransfer(context.Background(), ModeMove, src, dst,
		scanned{hash: hashOf("hello"), size: 5, modifiedAt: "2000-01-01T00:00:00.000000000Z"}, recorder(&committed))
	if !errors.Is(err, db.ErrChecksumMismatch) {
		t.Errorf("err = %v, want a checksum mismatch", err)
	}
	if len(committed) != 0 {
		t.Errorf("committed %v", committed)
	}
	if got, err := os.ReadFile(src); err != nil || string(got) != "HELLO" {
		t.Errorf("source = %q, %v; want it kept", got, err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a changed source landed in the library: %v", err)
	}
}

// A copy reports the source's size and lands at the planned name.
func TestTransferCopyReportsSize(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "A.jpg")
	writeFile(t, src, "hello")
	dst := filepath.Join(dir, "lib", "A.jpg")

	var committed []string
	landed, size, err := productionTransfer(context.Background(), ModeCopy, src, dst,
		scanned{hash: hashOf("hello"), size: 5}, recorder(&committed))
	if err != nil {
		t.Fatal(err)
	}
	if landed != dst || size != 5 || len(committed) != 1 {
		t.Errorf("landed %s, size %d, committed %v", landed, size, committed)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("copy touched the source: %v", err)
	}
}

// A move never deletes a source the taken name only appears to hold: a
// symlink to the source, or the source's own name.
func TestTransferMoveKeepsTheOnlyCopy(t *testing.T) {
	cases := map[string]func(t *testing.T, src string) (dst string){
		"symlink to the source": func(t *testing.T, src string) string {
			dst := filepath.Join(filepath.Dir(src), "lib", "A.jpg")
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(src, dst); err != nil {
				t.Skipf("no symlinks here: %v", err)
			}
			return dst
		},
		"the source itself": func(t *testing.T, src string) string { return src },
	}
	for name, dstFor := range cases {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "A.jpg")
			writeFile(t, src, "photo")
			info, err := os.Stat(src)
			if err != nil {
				t.Fatal(err)
			}
			dst := dstFor(t, src)

			var committed []string
			landed, _, err := productionTransfer(context.Background(), ModeMove, src, dst,
				scanned{hash: hashOf("photo"), size: 5, modifiedAt: db.FormatTime(info.ModTime())}, recorder(&committed))
			if err != nil {
				t.Fatal(err)
			}
			li, err := os.Lstat(landed)
			if err != nil || !li.Mode().IsRegular() {
				t.Fatalf("landed at %s (%v, %v); want a regular file", landed, li, err)
			}
			if got, err := os.ReadFile(landed); err != nil || string(got) != "photo" {
				t.Errorf("landed file = %q, %v; want the photo", got, err)
			}
			if len(committed) != 1 || committed[0] != landed {
				t.Errorf("committed %v, want only %s", committed, landed)
			}
		})
	}
}

// A move whose source is gone has nothing to copy: the failure is the
// rename's, and no copy is attempted.
func TestPlaceMoveWithVanishedSourceDoesNotCopy(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "lib", "A.jpg")
	err := place(ModeMove, filepath.Join(dir, "gone.jpg"), dst, hashOf("photo"), func() error {
		t.Error("committed a file that never moved")
		return nil
	})
	if failedOp(err) != opRename {
		t.Errorf("err = %v (op %s), want a rename failure", err, failedOp(err))
	}
}
