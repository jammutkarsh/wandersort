// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

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

// transfer lands one file: src at dst, or at the next free _N name beside it
// when dst is taken, and returns where it landed and the source's size. Every
// decision about that one file is behind this seam — a source gone because an
// earlier run already landed it, a source changed since the scan, a taken
// name already holding this file, a copy whose bytes don't hash to scan.hash
// (never lands). Atomic: the landing path either does not exist, or holds the
// complete file. Run only keeps the books. Two implementations:
// productionTransfer and dryRunTransfer.
//
// commit is called once the file is at its landing path and verified, and
// before a move unlinks the source. That order is the whole point: the
// database records the file as being in the library before its only other
// copy is destroyed, so a crash between the two leaves a duplicate rather
// than nothing. An error from commit means the file stays where it is and the
// source is kept. An error after a successful commit (a source that could not
// be removed) means the file is placed but the transfer did not finish
// cleanly.
type transfer func(ctx context.Context, mode Mode, src, dst string, scan scanned, commit commitFn) (landed string, bytes int64, err error)

// commitFn records one file as placed at landed, durably, before the caller
// does anything it cannot take back.
type commitFn func(landed string) error

// moveCopying is ModeMove for one source whose size or date changed since the
// scan. A same-device rename would land bytes nothing checked against the
// stored hash, so the file goes through the hash-checked copy instead and the
// source is removed after, as a move across devices is. Chosen per file by
// productionTransfer, never a Run's own Mode.
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

// dryRunTransfer touches no file. It answers what a real run can know without
// writing — the source's size, and whether a missing source already landed —
// and reports the planned name: a real run may land on name_N instead if that
// name is taken on disk. commit is still called, and still writes nothing: it
// is what counts the row as done.
func dryRunTransfer(_ context.Context, _ Mode, src, dst string, scan scanned, commit commitFn) (string, int64, error) {
	info, err := os.Stat(src)
	if err != nil {
		landed, err := recoverLanded(dst, scan, err, commit)
		return landed, 0, err
	}
	return dst, info.Size(), commit(dst)
}

// recoverLanded decides a source that is gone. A missing source is not
// automatically a failure: a crash after the file landed but before its row
// committed leaves exactly this, and a move has already deleted the original.
// The library is asked whether it holds the file before the row is written
// off — the alternative is a TRANSFER error that is never retried on a file
// sitting there, correct, all along. A missing source with nothing in the
// library is a plain failure.
func recoverLanded(dst string, scan scanned, statErr error, commit commitFn) (string, error) {
	if landed, ok := alreadyLanded(dst, scan); ok {
		return landed, commit(landed)
	}
	return dst, &stepError{opStat, fmt.Errorf("source missing: %w", statErr)}
}

// maxLandedProbe bounds the search for an already-landed file: dst and the
// _N names beside it up to this. A library with more collisions than this on
// one name has a bigger problem than a missed match.
const maxLandedProbe = 64

// alreadyLanded answers the one question a missing source leaves open: did
// this file already land, and only the row saying so go missing? It looks at
// the planned name and the _N names beside it for a copy of the scanned file
// (isCopy). Nothing else can tell a crashed-mid-run file apart from a source
// the user deleted, and the two deserve opposite answers.
//
// Every name up to maxLandedProbe is looked at, not just the unbroken run
// from dst: a _1 someone deleted leaves a gap, and the file can still be
// sitting at _2.
func alreadyLanded(dst string, scan scanned) (string, bool) {
	for n := 0; n < maxLandedProbe; n++ {
		if p := withSuffix(dst, n); isCopy(p, scan.size, scan.hash) {
			return p, true
		}
	}
	return "", false
}

// isCopy reports whether p holds a file of size bytes hashing to want. Size
// first, so a missing name costs one stat and an unrelated file taking the
// name is never read.
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

// errSourceNotRemoved means the file landed, verified, but a move could not
// remove its source. The file counts as placed anyway: the library holds it,
// and commit has already recorded it there.
var errSourceNotRemoved = errors.New("placed, but the source could not be removed")

// placeFree places src at dst, or at the first free dst_N beside it: nothing
// on disk is ever replaced (spec D21). vfs.Confirm already made every planned
// path unique among the rows it knows; this is the net for files the database
// doesn't know about. The link-based atomicfile.Rename/Copy fail with
// fs.ErrExist instead of overwriting, and that is the only error that moves
// on to the next name.
//
// A taken name already holding this very file is where it lands: a crash
// between an earlier copy landing and its row committing, or `wandersort
// admin db --restore` putting back rows as pending whose files are already
// placed. Taking the next _N there would place the file twice.
func placeFree(ctx context.Context, mode Mode, src, dst, want string, commit commitFn) (string, error) {
	if err := ctx.Err(); err != nil {
		return dst, err
	}
	if err := atomicfile.MkdirAll(filepath.Dir(dst)); err != nil {
		return dst, &stepError{opMkdir, fmt.Errorf("create dest dir: %w", err)}
	}
	for n := 0; ; n++ {
		target := withSuffix(dst, n)
		// A taken name is seen before a byte is copied: a copy only learns
		// it at the final link, so every collision used to cost the whole
		// file again. The link still refuses a name taken in between.
		var err error
		if _, statErr := os.Lstat(target); statErr == nil {
			err = fs.ErrExist
		} else {
			err = placeFile(mode, src, target, want, func() error { return commit(target) })
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
		// The library copy matching the scan says nothing about the
		// source: an edit since then, same size, would be deleted here
		// for good (spec D22). Rare path, so the extra read is cheap.
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

// placeFile is place, called through a variable so a test can see which
// names placeFree tries to write to.
var placeFile = place

// place puts src at exactly dst, failing with fs.ErrExist if dst is taken.
// Move tries a same-device no-replace rename first — atomic, nothing copied;
// productionTransfer only allows it for a source whose size and date still
// match the scan (moveCopying otherwise), which is the check a rename gets.
// Any other failure (cross-device, mostly) falls back to copy, except a
// source that can't be removed, which a copy would fail on too. A copy hashes
// the bytes as it writes them and lands only if they hash to want (spec D22)
// — a source changed since the scan is not the file that was planned. Copy
// never unlinks src; Move only does once the copy is verified and commit has
// recorded it, so the source is never lost to a partial write, a wrong write,
// or a row that never made it to disk, and an occupied dst never loses it at
// all.
func place(mode Mode, src, dst, want string, commit func() error) error {
	if mode == ModeMove {
		// The commit sits inside the rename, between the new name landing
		// and the old one going: the database records the file in the
		// library while it still has its source name too. Committing after
		// the rename instead left a crash window with the file moved and
		// nothing recording it — and a scan before the next execute then
		// swept the source's row, leaving a library file nothing tracks.
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
	// The file is in the library and verified. Record it before the source is
	// unlinked: the other order leaves a crash holding a library file nothing
	// knows about and no source to re-read it from.
	if err := commit(); err != nil {
		// Not recorded, so not kept: the source is untouched, and a library
		// copy no row accounts for would sit beside the retry's own. This
		// name was free until the copy above took it, so it is ours to remove.
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
