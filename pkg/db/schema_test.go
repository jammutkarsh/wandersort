// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package db_test

import (
	"context"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

// The schema, not the reading code's ordering, keeps a file to one metadata
// row: a second would list the file twice in every join.
func TestSchemaOneMetadataRowPerFile(t *testing.T) {
	d := dbtest.New(t)
	dbtest.SeedFile(t, d, 1, "/src", "a.jpg", 1)
	dbtest.SeedHash(t, d, 1, "blake3:aa")
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO file_metadata (file_hash, file_id) VALUES ('blake3:bb', 1)`); err == nil {
		t.Error("a second metadata row for one file was accepted")
	}
}
