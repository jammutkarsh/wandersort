package tui

import (
	"context"
	"errors"
	"io/fs"
	"syscall"

	"github.com/jammutkarsh/wandersort/pkg/lock"
)

// explanations is the plain sentence for the system errors people meet most, in match order.
var explanations = []struct {
	cause    error
	sentence string
}{
	{context.Canceled, "Stopped."},
	{lock.ErrHeld, "WanderSort is already open on this library. Close the other window first."},
	{syscall.ENOSPC, "The drive is full. Free some space, then try again."},
	{syscall.EROFS, "That drive is read-only. Pick a different folder or drive."},
	{fs.ErrPermission, "WanderSort isn't allowed to use that folder. Check the folder's permissions, then try again."},
	{fs.ErrNotExist, "That folder or file isn't there. If it is on a drive, check the drive is plugged in."},
	{syscall.EIO, "The drive stopped answering. Check its cable, then try again."},
	{syscall.ENODEV, "The drive stopped answering. Check its cable, then try again."},
}

// Explain is err as a sentence a person can act on. Errors it does not know
// are already worded for people, so they pass through; the log keeps every cause.
func Explain(err error) string {
	for _, e := range explanations {
		if errors.Is(err, e.cause) {
			return e.sentence
		}
	}
	return err.Error()
}
