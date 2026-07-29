// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package lock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryFlock attempts a single non-blocking exclusive flock on f. Returns
// ErrHeld if another open file description already holds it.
func tryFlock(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrHeld
	}
	return err
}
