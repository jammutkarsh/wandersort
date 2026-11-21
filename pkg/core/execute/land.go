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
	hash string
	size int64
}

// transfer copies src to dst (or the next free _N name) atomically and returns
// where it landed and the source's size. The source is never modified.
// Implementations are productionTransfer and dryRunTransfer.
//
// commit runs once the copy is verified at its landing path; a commit error
// removes the copy.
type transfer func(ctx context.Context, src, dst string, scan scanned, commit commitFn) (landed string, bytes int64, err error)

// commitFn records one file as placed at landed, durably.
type commitFn func(landed string) error

// productionTransfer is the transfer that touches the disk.
func productionTransfer(ctx context.Context, src, dst string, scan scanned, commit commitFn) (string, int64, error) {
	info, err := os.Stat(src)
	if err != nil {
		landed, err := recoverLanded(ctx, dst, scan, err, commit)
		return landed, 0, err
	}
	landed, err := placeFree(ctx, src, dst, scan.hash, commit)
	return landed, info.Size(), err
}

// dryRunTransfer touches no file: it reports the source's size and the planned
// name (a real run may land on name_N), and calls commit to count the row.
func dryRunTransfer(ctx context.Context, src, dst string, scan scanned, commit commitFn) (string, int64, error) {
	info, err := os.Stat(src)
	if err != nil {
		landed, err := recoverLanded(ctx, dst, scan, err, commit)
		return landed, 0, err
	}
	return dst, info.Size(), commit(dst)
}

// recoverLanded handles a missing source: if the library already holds the file
// (a crash after landing, before the row committed), record it placed;
// otherwise fail.
func recoverLanded(ctx context.Context, dst string, scan scanned, statErr error, commit commitFn) (string, error) {
	if landed, ok := alreadyLanded(ctx, dst, scan); ok {
		return landed, commit(landed)
	}
	if err := ctx.Err(); err != nil {
		return dst, err // a search stopped partway proves nothing missing
	}
	return dst, &stepError{opStat, fmt.Errorf("source missing: %w", statErr)}
}

// maxLandedProbe bounds the _N names alreadyLanded checks.
const maxLandedProbe = 64

// alreadyLanded looks for a copy of the scanned file at dst and every _N name
// up to maxLandedProbe (gaps allowed).
func alreadyLanded(ctx context.Context, dst string, scan scanned) (string, bool) {
	for n := 0; n < maxLandedProbe && ctx.Err() == nil; n++ {
		if p := withSuffix(dst, n); isCopy(ctx, p, scan.size, scan.hash) {
			return p, true
		}
	}
	return "", false
}

// isCopy reports whether p is a regular file of size bytes hashing to want;
// size first, so a mismatch is never read. A symlink is never a copy: it may
// point at the source itself. A read stopped by ctx is not a copy.
func isCopy(ctx context.Context, p string, size int64, want string) bool {
	if want == "" {
		return false
	}
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return false
	}
	got, err := metadata.HashFile(ctx, p)
	return err == nil && got == want
}

// placeFree copies src to dst or the first free dst_N: nothing on disk is ever
// replaced. Only fs.ErrExist moves on to the next name. A taken name already
// holding this file is where it lands (a crash before the row committed, or a
// restore re-pending placed rows), so it is never placed twice.
func placeFree(ctx context.Context, src, dst, want string, commit commitFn) (string, error) {
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
			err = place(ctx, src, target, want, func() error { return commit(target) })
		}
		if !errors.Is(err, fs.ErrExist) {
			return target, err
		}
		if holds(ctx, target, src, want) {
			return target, commit(target)
		}
		if err := ctx.Err(); err != nil {
			return target, err
		}
	}
}

// holds reports whether p is already a copy of src: a copy of the scanned
// file the size src is now.
func holds(ctx context.Context, p, src, want string) bool {
	si, err := os.Stat(src)
	return err == nil && isCopy(ctx, p, si.Size(), want)
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

// place copies src to exactly dst, failing with fs.ErrExist if dst is taken.
// The copy lands only if the bytes read from src and the bytes read back from
// the library both hash to want, and is removed again if it
// was surely not recorded. A commit of unknown outcome keeps it: the record may
// be on disk, and if not, the next run finds the copy at this name and records it.
func place(ctx context.Context, src, dst, want string, commit func() error) error {
	h := metadata.NewHasher()
	if _, err := atomicfile.CopyContext(ctx, src, dst, h, func(tmp string) error {
		if got := metadata.HashString(h); got != want {
			return fmt.Errorf("source changed since it was scanned: copied %s, scanned %q: %w", got, want, db.ErrChecksumMismatch)
		}
		// the hash above saw the bytes read from the source; read back what
		// the library's drive holds, so a bad write never lands
		if got, err := metadata.HashFile(ctx, tmp); err != nil {
			return fmt.Errorf("read back the copy: %w", err)
		} else if got != want {
			return fmt.Errorf("the copy reads back as %s, not the source's %s: %w", got, want, db.ErrChecksumMismatch)
		}
		return nil
	}); err != nil {
		return &stepError{opCopy, err}
	}
	if err := commit(); err != nil {
		if errors.Is(err, db.ErrCommitUnknown) {
			return fmt.Errorf("the copy stays at %s until the next copy records it: %w", dst, err)
		}
		// not recorded, so not kept: this name was free until the copy took it
		if rerr := os.Remove(dst); rerr != nil {
			return errors.Join(err, fmt.Errorf("the copy stays at %s, unrecorded: %w", dst, rerr))
		}
		return err
	}
	return nil
}

// Steps of a transfer, as recorded in errors.op.
const (
	opStat   = "stat"
	opMkdir  = "mkdir"
	opCopy   = "copy"
	opCommit = "commit"
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
