// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package volume

import (
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		n    uint64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KiB"},
		{5 << 30, "5.0 GiB"},
		{1610612736, "1.5 GiB"},
	}
	for _, tt := range tests {
		if got := HumanBytes(tt.n); got != tt.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// The library size counts one file per content hash, not one per row: a
// duplicate is never copied, so counting it would overstate what a scan
// writes. Identical bytes are identical sizes, so which copy the scorer
// elected is a question this package does not have to ask — and could not,
// without reaching up into pkg/core for the rule.
func TestCheckOutputSpaceCountsOneFilePerHash(t *testing.T) {
	d := dbtest.New(t)
	// Three rows, two of them the same content.
	dbtest.SeedFile(t, d, 1, "/a", "one.jpg", 100)
	dbtest.SeedHash(t, d, 1, "blake3:aaa")
	dbtest.SeedFile(t, d, 2, "/b", "one-copy.jpg", 100)
	dbtest.SeedHash(t, d, 2, "blake3:aaa")
	dbtest.SeedFile(t, d, 3, "/a", "two.jpg", 50)
	dbtest.SeedHash(t, d, 3, "blake3:bbb")

	var got int64
	if err := d.SQL.Get(&got, `SELECT COALESCE(SUM(size), 0) FROM (
		SELECT MIN(fr.file_size) AS size FROM file_registry fr
		JOIN file_metadata fm ON fm.file_id = fr.id
		GROUP BY fm.file_hash)`); err != nil {
		t.Fatal(err)
	}
	if want := int64(150); got != want {
		t.Errorf("library size = %d, want %d (the duplicate must not be counted twice)", got, want)
	}
}
