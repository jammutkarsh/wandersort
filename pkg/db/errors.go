// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
)

// Stages of the errors table. Planning is library-wide, so it fails the run,
// not a file — these are the three that can fail one file: reading it,
// transferring it, and checking afterwards that it is still what was
// recorded.
const (
	StageRead     = "READ"
	StageTransfer = "TRANSFER"
	StageVerify   = "VERIFY"
)

// Kinds an error row is bucketed into, so a report can group them.
const (
	KindPermissionDenied = "permission-denied"
	KindNotFound         = "not-found"
	KindIO               = "io-error"
	KindNoSpace          = "no-space"
	KindChecksumMismatch = "checksum-mismatch"
	KindPanic            = "panic"
	KindOther            = "other"
)

// ErrChecksumMismatch marks a copy whose bytes do not hash to what the scan
// stored; RecordError files it under KindChecksumMismatch.
var ErrChecksumMismatch = errors.New("checksum mismatch")

// maxFrames bounds the frames kept per error: enough to name the code path.
const maxFrames = 16

// Frame is one call site in a recorded stack.
type Frame struct {
	Func string `json:"func"`
	File string `json:"file"`
	Line int    `json:"line"`
}

// stackError is an error that remembers where the pipeline recorded it.
type stackError struct {
	err    error
	frames []Frame
	panic  bool
}

func (e *stackError) Error() string { return e.err.Error() }
func (e *stackError) Unwrap() error { return e.err }

// WithStack attaches the caller's stack to err. Go errors carry none, and a
// failure is usually recorded from the BulkWriter's goroutine, so the frames
// have to be taken where the failure is seen, before the write is enqueued.
func WithStack(err error) error {
	if err == nil {
		return nil
	}
	return &stackError{err: err, frames: callers(3)}
}

// PanicError wraps a recovered panic value with the real stack of the panic.
// Call it directly from the deferred function that called recover.
func PanicError(recovered any) error {
	return &stackError{err: fmt.Errorf("panic: %v", recovered), frames: callers(3), panic: true}
}

// callers returns the frames above skip, package-internal ones included.
func callers(skip int) []Frame {
	pcs := make([]uintptr, maxFrames)
	pcs = pcs[:runtime.Callers(skip, pcs)]
	var frames []Frame
	it := runtime.CallersFrames(pcs)
	for {
		f, more := it.Next()
		frames = append(frames, Frame{Func: f.Function, File: moduleRelative(f.File), Line: f.Line})
		if !more {
			return frames
		}
	}
}

// modulePath is the directory name the repository is checked out under, and so
// where a frame's file path turns relative: the build machine's directories
// name nobody worth knowing about.
const modulePath = "wandersort/"

// moduleRelative shortens a frame's file to its path inside the module. Files
// outside it (the standard library) keep the path they have.
func moduleRelative(file string) string {
	if i := strings.LastIndex(file, modulePath); i >= 0 {
		return file[i+len(modulePath):]
	}
	return file
}

// chainLink is one layer of the unwrapped error.
type chainLink struct {
	Layer int    `json:"layer"`
	Error string `json:"error"`
}

// syscallInfo is present in the detail only when the error carries an errno.
type syscallInfo struct {
	Errno string `json:"errno"`
	Op    string `json:"op,omitempty"`
}

// ErrorDetail is what error.detail stores, every part a named field.
type ErrorDetail struct {
	Message string       `json:"message"`
	Chain   []chainLink  `json:"chain"`
	Frames  []Frame      `json:"frames"`
	Syscall *syscallInfo `json:"syscall,omitempty"`
}

// errnoNames names the errnos the pipeline can meet. Any other is shown by
// its number, since x/sys has no portable name table.
var errnoNames = map[syscall.Errno]string{
	syscall.EACCES: "EACCES", syscall.EPERM: "EPERM", syscall.ENOENT: "ENOENT",
	syscall.ENOSPC: "ENOSPC", syscall.EIO: "EIO",
}

// errorKind buckets err.
func errorKind(err error, panicked bool) string {
	switch {
	case panicked:
		return KindPanic
	case errors.Is(err, fs.ErrPermission):
		return KindPermissionDenied
	case errors.Is(err, fs.ErrNotExist):
		return KindNotFound
	case errors.Is(err, syscall.ENOSPC):
		return KindNoSpace
	case errors.Is(err, syscall.EIO):
		return KindIO
	case errors.Is(err, ErrChecksumMismatch):
		return KindChecksumMismatch
	}
	return KindOther
}

// describe builds the stored detail of err: the message, the chain walked to
// the bottom (joined errors are one layer, their text), the frames and, when
// present, the errno.
func describe(err error) (detail ErrorDetail, panicked bool) {
	detail = ErrorDetail{Message: err.Error(), Chain: []chainLink{}}
	for cur := err; cur != nil; cur = errors.Unwrap(cur) {
		if stack, ok := cur.(*stackError); ok {
			// the wrapper adds no text of its own; carry its frames instead
			detail.Frames, panicked = stack.frames, stack.panic
			continue
		}
		detail.Chain = append(detail.Chain, chainLink{Layer: len(detail.Chain), Error: cur.Error()})
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		name, ok := errnoNames[errno]
		if !ok {
			name = fmt.Sprintf("errno %d", uintptr(errno))
		}
		detail.Syscall = &syscallInfo{Errno: name}
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			detail.Syscall.Op = pathErr.Op
		}
	}
	if detail.Frames == nil {
		detail.Frames = callers(3)
	}
	return detail, panicked
}

// RecordError stores err as file fileID's failure for stage, replacing an
// earlier one and counting the attempt. op is the step that failed. Frames
// come from WithStack/PanicError when err carries them, else from here, which
// is only meaningful when the caller is the failing code itself (a
// synchronous transaction, not a queued write).
func RecordError(ctx context.Context, tx *sqlx.Tx, fileID int64, stage, op string, err error) error {
	detail, panicked := describe(err)
	// An error names a file, and a Linux file name need not be UTF-8: store
	// it with the bad bytes replaced rather than lose the failure record.
	raw, marshalErr := json.Marshal(detail, jsontext.AllowInvalidUTF8(true))
	if marshalErr != nil {
		return fmt.Errorf("record error: %w", marshalErr)
	}
	now := FormatTime(time.Now())
	if _, execErr := tx.ExecContext(ctx, `
		INSERT INTO errors (file_id, stage, op, kind, detail, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (file_id, stage) DO UPDATE SET
			op = excluded.op, kind = excluded.kind, detail = excluded.detail,
			attempts = errors.attempts + 1, last_seen_at = excluded.last_seen_at`,
		fileID, stage, op, errorKind(err, panicked), string(raw), now, now); execErr != nil {
		return fmt.Errorf("record error: %w", execErr)
	}
	return nil
}
