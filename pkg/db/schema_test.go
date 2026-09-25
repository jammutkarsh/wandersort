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

// user_labels is a set of names: the same name twice is one row.
func TestSchemaLabelsAreASet(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	if _, err := d.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('Goa', 'EVENT')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO user_labels (label, kind) VALUES ('Goa', 'EVENT')`); err == nil {
		t.Error("the same label was stored twice")
	}
}

// Tables are STRICT: a value of the wrong type is refused when it is written,
// instead of being stored and surfacing later as a wrong total or a
// timestamp that sorts out of place.
func TestSchemaRefusesWrongTypes(t *testing.T) {
	d := dbtest.New(t)
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO file_registry (file_dir, file_name, file_size, file_modified_at,
			file_extension, discovered_at, last_seen_at)
		VALUES ('/src', 'a.jpg', '12 KB', 't', '.jpg', 't', 't')`); err == nil {
		t.Error("a text file_size was stored")
	}
}
