package execute

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/atomicfile"
	"github.com/jammutkarsh/wandersort/pkg/core/metadata"
	"github.com/jammutkarsh/wandersort/pkg/db"
)

// scanned is what the scan recorded about a source file: the facts a landing
// checks the file on disk against.
type scanned struct {
	hash       string
	size       int64
	modifiedAt string // db.FormatTime of the file's mtime
}

// changed reports whether the file at info is not the one the scan saw.
func (s scanned) changed(info os.FileInfo) bool {
	return info.Size() != s.size || db.FormatTime(info.ModTime()) != s.modifiedAt
}

// transfer lands src at dst (or the next free _N name) atomically and returns
// where it landed and the source's size. Every per-file decision lives behind
// it; implementations are productionTransfer and dryRunTransfer.
//
// commit runs once the file is verified at its landing path and before a move
// unlinks src, so a crash leaves a duplicate, never a loss. A commit error
// keeps the source; an error after commit means placed but not cleanly.
type transfer func(ctx context.Context, mode Mode, src, dst string, scan scanned, commit commitFn) (landed string, bytes int64, err error)

// commitFn records one file as placed at landed, durably, before the caller
// does anything it cannot take back.
type commitFn func(landed string) error

// moveCopying is a move for a source changed since the scan: hash-checked copy,
// then remove the source, instead of an unchecked rename.
const moveCopying Mode = -1

// productionTransfer is the transfer that touches the disk.
func productionTransfer(ctx context.Context, mode Mode, src, dst string, scan scanned, commit commitFn) (string, int64, error) {
	// Stat before transferring, not after: a move unlinks the source on
	// success, so "after" has nothing left to size.
	info, err := os.Stat(src)
	if err != nil {
		landed, err := recoverLanded(dst, scan, err, commit)
		return landed, 0, err
	}
	if mode == ModeMove && scan.changed(info) {
		mode = moveCopying
	}
	landed, err := placeFree(ctx, mode, src, dst, scan.hash, commit)
	return landed, info.Size(), err
}

// dryRunTransfer touches no file: it reports the source's size and the planned
// name (a real run may land on name_N), and calls commit to count the row.
func dryRunTransfer(_ context.Context, _ Mode, src, dst string, scan scanned, commit commitFn) (string, int64, error) {
	info, err := os.Stat(src)
	if err != nil {
		landed, err := recoverLanded(dst, scan, err, commit)
		return landed, 0, err
	}
	return dst, info.Size(), commit(dst)
}

// recoverLanded handles a missing source: if the library already holds the file
// (a crash after landing, before the row committed), record it placed;
// otherwise fail.
func recoverLanded(dst string, scan scanned, statErr error, commit commitFn) (string, error) {
	if landed, ok := alreadyLanded(dst, scan); ok {
		return landed, commit(landed)
	}
	return dst, &stepError{opStat, fmt.Errorf("source missing: %w", statErr)}
}

// maxLandedProbe bounds the _N names alreadyLanded checks.
const maxLandedProbe = 64

// alreadyLanded looks for a copy of the scanned file at dst and every _N name
// up to maxLandedProbe (gaps allowed).
func alreadyLanded(dst string, scan scanned) (string, bool) {
	for n := 0; n < maxLandedProbe; n++ {
		if p := withSuffix(dst, n); isCopy(p, scan.size, scan.hash) {
			return p, true
		}
	}
	return "", false
}

// isCopy reports whether p holds size bytes hashing to want; size first, so a
// mismatch is never read.
func isCopy(p string, size int64, want string) bool {
	if want == "" {
		return false
	}
	info, err := os.Stat(p)
	if err != nil || info.Size() != size {
		return false
	}
	got, err := metadata.HashFile(p)
	return err == nil && got == want
}

// errSourceNotRemoved means the file landed and was recorded, but a move could
// not remove its source.
var errSourceNotRemoved = errors.New("placed, but the source could not be removed")

// placeFree places src at dst or the first free dst_N: nothing on disk is ever
// replaced. Only fs.ErrExist moves on to the next name. A taken name already
// holding this file is where it lands (a crash before the row committed, or a
// restore re-pending placed rows), so it is never placed twice.
func placeFree(ctx context.Context, mode Mode, src, dst, want string, commit commitFn) (string, error) {
	if err := ctx.Err(); err != nil {
		return dst, err
	}
	if err := atomicfile.MkdirAll(filepath.Dir(dst)); err != nil {
		return dst, &stepError{opMkdir, fmt.Errorf("create dest dir: %w", err)}
	}
	for n := 0; ; n++ {
		target := withSuffix(dst, n)
		// check a taken name before copying a byte; the link still refuses a
		// name taken in between
		var err error
		if _, statErr := os.Lstat(target); statErr == nil {
			err = fs.ErrExist
		} else {
			err = place(mode, src, target, want, func() error { return commit(target) })
		}
		if !errors.Is(err, fs.ErrExist) {
			return target, err
		}
		if !holds(target, src, want) {
			continue
		}
		if !mode.moves() {
			return target, commit(target)
		}
		// the library copy matching says nothing about the source: an
		// edited source of the same size must not be deleted
		got, err := metadata.HashFile(src)
		if err != nil {
			return target, &stepError{opHash, fmt.Errorf("verify source before removing it: %w", err)}
		}
		if got != want {
			return target, &stepError{opHash, fmt.Errorf("source changed since it was scanned: source %s, scanned %q: %w", got, want, db.ErrChecksumMismatch)}
		}
		if err := commit(target); err != nil {
			return target, err
		}
		return target, removeSource(src)
	}
}

// holds reports whether p is already a copy of src: a copy of the scanned
// file the size src is now.
func holds(p, src, want string) bool {
	si, err := os.Stat(src)
	return err == nil && isCopy(p, si.Size(), want)
}

// removeSource finishes a move whose file is already verified in place.
func removeSource(src string) error {
	if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &stepError{opRemoveSource, fmt.Errorf("%w: %w", errSourceNotRemoved, err)}
	}
	return nil
}

// withSuffix names the n-th alternative for p: p itself, then name_1.ext,
// name_2.ext, …
func withSuffix(p string, n int) string {
	if n == 0 {
		return p
	}
	ext := filepath.Ext(p)
	return fmt.Sprintf("%s_%d%s", strings.TrimSuffix(p, ext), n, ext)
}

// place puts src at exactly dst, failing with fs.ErrExist if dst is taken. Move
// tries a same-device no-replace rename first (only for an unchanged source),
// else falls back to copy. A copy lands only if its bytes hash to want. A move
// unlinks src only after the copy is verified and committed.
func place(mode Mode, src, dst, want string, commit func() error) error {
	if mode == ModeMove {
		// commit runs inside the rename, after the new name lands and before
		// the old one goes, so no crash leaves a moved file unrecorded
		var commitErr error
		err := atomicfile.RenameCommit(src, dst, func() error {
			commitErr = commit()
			return commitErr
		})
		switch {
		case err == nil:
			return nil
		case commitErr != nil:
			return err // not recorded, so the move was undone
		case errors.Is(err, atomicfile.ErrSourceLeft):
			return &stepError{opRemoveSource, fmt.Errorf("%w: %w", errSourceNotRemoved, err)}
		case errors.Is(err, fs.ErrExist):
			return err
		case errors.Is(err, atomicfile.ErrSourceKept):
			return &stepError{opRename, err}
		}
		// anything else — another device, mostly — falls back to a copy
	}

	h := metadata.NewHasher()
	if _, err := atomicfile.Copy(src, dst, h, func() error {
		if got := metadata.HashString(h); got != want {
			return fmt.Errorf("source changed since it was scanned: copied %s, scanned %q: %w", got, want, db.ErrChecksumMismatch)
		}
		return nil
	}); err != nil {
		return &stepError{opCopy, err}
	}
	// record before unlinking the source
	if err := commit(); err != nil {
		// not recorded, so not kept: this name was free until the copy took it
		if rerr := os.Remove(dst); rerr != nil {
			return errors.Join(err, fmt.Errorf("the copy stays at %s, unrecorded: %w", dst, rerr))
		}
		return err
	}
	if !mode.moves() {
		return nil
	}
	return removeSource(src)
}

// Steps of a transfer, as recorded in errors.op.
const (
	opStat         = "stat"
	opMkdir        = "mkdir"
	opCopy         = "copy"
	opRename       = "rename"
	opHash         = "hash"
	opRemoveSource = "remove-source"
	opCommit       = "commit"
)

// stepError names the step of a transfer that failed.
type stepError struct {
	op  string
	err error
}

func (e *stepError) Error() string { return e.err.Error() }
func (e *stepError) Unwrap() error { return e.err }

// failedOp is the step xerr names, else copy — the step that moves the bytes.
func failedOp(xerr error) string {
	var step *stepError
	if errors.As(xerr, &step) {
		return step.op
	}
	return opCopy
}
