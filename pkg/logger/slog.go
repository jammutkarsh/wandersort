// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package logger

import (
	"log/slog"
	"strings"
)

func getSlogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "local", "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "dev", "warn":
		return slog.LevelWarn
	case "prod", "error":
		return slog.LevelError
	default:
		return slog.LevelDebug
	}
}
