// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin

package volume

import "testing"

// plistOf builds the fragment of `diskutil info -plist` output the class
// ladder actually reads, so a case states only the keys it cares about
func plistOf(pairs ...string) []byte {
	out := "<plist><dict>"
	for i := 0; i < len(pairs); i += 2 {
		out += "<key>" + pairs[i] + "</key>"
		switch v := pairs[i+1]; v {
		case "true", "false":
			out += "<" + v + "/>"
		default:
			out += "<string>" + v + "</string>"
		}
	}
	return []byte(out + "</dict></plist>")
}

func TestClassFromDiskutil(t *testing.T) {
	tests := []struct {
		name  string
		plist []byte
		want  Class
	}{
		{
			name:  "InternalNVMe",
			plist: plistOf("SolidState", "true", "Internal", "true", "BusProtocol", "Apple Fabric"),
			want:  ClassSolidState,
		},
		{
			name:  "ThunderboltExternalSSD",
			plist: plistOf("SolidState", "true", "Internal", "false", "BusProtocol", "Thunderbolt"),
			want:  ClassSolidState,
		},
		{
			name:  "USBThumbDrive",
			plist: plistOf("SolidState", "true", "Ejectable", "true", "BusProtocol", "USB"),
			want:  ClassRemovable,
		},
		{
			name:  "ExternalSpinningDisk",
			plist: plistOf("SolidState", "false", "BusProtocol", "USB"),
			want:  ClassRotational,
		},
		{
			// a RAID can be mixed, so the slow read is the safe read — even
			// when the plist claims solid state
			name:  "RAIDMasterBeatsSolidState",
			plist: plistOf("RAIDMaster", "true", "SolidState", "true", "BusProtocol", "Thunderbolt"),
			want:  ClassRotational,
		},
		{
			name:  "NoSolidStateKey",
			plist: plistOf("BusProtocol", "USB"),
			want:  ClassUnknown,
		},
		{
			name:  "Empty",
			plist: []byte(""),
			want:  ClassUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classFromDiskutil(tt.plist); got != tt.want {
				t.Errorf("classFromDiskutil() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlistReaders(t *testing.T) {
	p := plistOf("SolidState", "true", "Internal", "false", "BusProtocol", "USB")

	if v, ok := plistBool(p, "SolidState"); !ok || !v {
		t.Errorf("plistBool(SolidState) = %v, %v; want true, true", v, ok)
	}
	if v, ok := plistBool(p, "Internal"); !ok || v {
		t.Errorf("plistBool(Internal) = %v, %v; want false, true", v, ok)
	}
	if _, ok := plistBool(p, "Missing"); ok {
		t.Error("plistBool(Missing) reported found")
	}
	if v, ok := plistString(p, "BusProtocol"); !ok || v != "USB" {
		t.Errorf("plistString(BusProtocol) = %q, %v; want \"USB\", true", v, ok)
	}
	if _, ok := plistString(p, "Missing"); ok {
		t.Error("plistString(Missing) reported found")
	}
}
