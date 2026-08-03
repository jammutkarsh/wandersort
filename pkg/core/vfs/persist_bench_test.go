// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// seedRegistry inserts n file_registry rows in one statement — virtual_fs_entries
// has an FK onto it, so persist needs them to exist.
func seedRegistry(b *testing.B, d *db.DB, n int) {
	b.Helper()
	var sb strings.Builder
	sb.WriteString(`INSERT INTO file_registry (id, file_dir, file_name, file_size, file_modified_at,
		file_extension, media_type, discovered_at, last_seen_at) VALUES `)
	for i := range n {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `(%d, '/src/trip%d', 'IMG_%05d.HEIC', 1024, '2024-06-01T10:00:00.000000000Z',
			'.HEIC', 'IMAGE', '2024-06-01T10:00:00.000000000Z', '2024-06-01T10:00:00.000000000Z')`,
			i+1, i%20, i)
	}
	if _, err := d.ExecContext(context.Background(), sb.String()); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkPersist times the phase's write-out on its own — the other half of
// the vfs phase's wall time, and the only part that touches the app database.
func BenchmarkPersist(b *testing.B) {
	const n = 20000
	d := dbtest.New(b)
	seedRegistry(b, d, n)
	v := &VFS{db: d, log: logger.NewNoopLogger(), cfg: DefaultConfig()}

	masters := benchMasters(n)
	for i := range masters {
		masters[i].targetPath = fmt.Sprintf("2024/06_June/%02d/Goa/Apple iPhone 15 Pro/IMG_%05d.HEIC", i%28+1, i)
		masters[i].clusterID = fmt.Sprintf("c%d", i%50)
		masters[i].locationDir = "Goa"
	}

	for b.Loop() {
		if _, err := v.persist(masters); err != nil {
			b.Fatal(err)
		}
		d.Writer.Flush()
	}
}
