// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package report

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/db/dbtest"
)

func TestScrub(t *testing.T) {
	const home = "/Users/zzuser"
	known := knownPaths{
		Options: Options{Home: home, Library: "/Volumes/Photos/Library", Redact: false},
		source:  "/Users/zzuser/Pictures/Import/IMG_0231.HEIC",
		name:    "IMG_0231.HEIC",
		target:  "2024/06_June/IMG_0231.HEIC",
	}
	msg := "hash /Users/zzuser/Pictures/Import/IMG_0231.HEIC: open /Users/zzuser/Pictures/Import/IMG_0231.HEIC: permission denied"
	tests := []struct {
		name    string
		redact  bool
		in      string
		want    string
		notWant []string
	}{
		{
			"home becomes $HOME", false, msg,
			"hash $HOME/Pictures/Import/IMG_0231.HEIC: open $HOME/Pictures/Import/IMG_0231.HEIC: permission denied",
			[]string{"zzuser"},
		},
		{
			"outside home is left alone", false, "open /Volumes/SD/DCIM/IMG_1.HEIC: no such file",
			"open /Volumes/SD/DCIM/IMG_1.HEIC: no such file", nil,
		},
		{"a longer name sharing the prefix is someone else's", false, "open /Users/zzuserie/x", "open /Users/zzuserie/x", nil},
		{
			"redact leaves no path", true,
			"copy /Users/zzuser/Pictures/Import/IMG_0231.HEIC to /Volumes/Photos/Library/2024/06_June/IMG_0231.HEIC: mkdir /Users/zzuser/Other/dir: denied",
			"copy <source> to <target>: mkdir <path>: denied",
			[]string{"/Users", "zzuser", "Volumes", "IMG_0231"},
		},
		{
			"redact takes a path with spaces whole", true,
			"open /Users/zzuser/Pictures/Goa Trip 2024/Mum (2).jpg: permission denied",
			"open <path>: permission denied",
			[]string{"Goa", "Trip", "Mum"},
		},
		{"redact keeps a module-relative frame file", true, "pkg/core/metadata/metadata.go", "pkg/core/metadata/metadata.go", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := known
			k.Redact = tt.redact
			got := k.scrub(tt.in)
			if got != tt.want {
				t.Errorf("scrub = %q, want %q", got, tt.want)
			}
			for _, bad := range tt.notWant {
				if strings.Contains(got, bad) {
					t.Errorf("scrub left %q in %q", bad, got)
				}
			}
		})
	}
}

// The username appears nowhere in what is exported, in any string of the
// stored detail; with --redact-paths no path does either.
func TestErrorsExport(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	dbtest.SeedFile(t, d, 1, "/Users/zzuser/Pictures/Import", "IMG_0231.HEIC", 2411008)

	failure := db.WithStack(fmt.Errorf("hash /Users/zzuser/Pictures/Import/IMG_0231.HEIC: %w",
		&fs.PathError{Op: "open", Path: "/Users/zzuser/Pictures/Import/IMG_0231.HEIC", Err: fs.ErrPermission}))
	for range 2 {
		if !d.Writer.Write(func(ctx context.Context, tx *sqlx.Tx) error {
			return db.RecordError(ctx, tx, 1, db.StageRead, "hash", failure)
		}) {
			t.Fatal("writer closed")
		}
	}
	d.Writer.Flush()

	for _, redact := range []bool{false, true} {
		rows, summary, err := Errors(ctx, d.SQL, Options{Home: "/Users/zzuser", Library: "/Volumes/L", Redact: redact})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Kind != db.KindPermissionDenied || rows[0].Attempts != 2 {
			t.Fatalf("rows = %+v, want one permission-denied row after 2 attempts", rows)
		}
		if len(summary) != 1 || !strings.HasPrefix(summary[0], "1 x READ/hash/permission-denied at ") ||
			!strings.HasSuffix(summary[0], ", .HEIC, "+rows[0].File.VolumeClass) {
			t.Errorf("summary = %v", summary)
		}
		out := string(rows[0].Detail)
		if strings.Contains(out, "zzuser") {
			t.Errorf("redact=%v: the username survived in %s", redact, out)
		}
		if redact && strings.Contains(out, "/Users") {
			t.Errorf("redact=%v: a path survived in %s", redact, out)
		}
		if !redact && !strings.Contains(out, "$HOME/Pictures/Import/IMG_0231.HEIC") {
			t.Errorf("default export lost the folder structure: %s", out)
		}
	}
}
