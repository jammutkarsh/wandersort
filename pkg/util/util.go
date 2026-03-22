// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package util

import (
	"log"
	"os"
)

type Util struct {
	HomeDir string
}

func NewUtil() *Util {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Panic("Failed to get user home directory", "error", err)
	}
	return &Util{HomeDir: home}
}
