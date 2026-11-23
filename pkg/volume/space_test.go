package volume

import "testing"

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		n    uint64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KB"},
		{288_000, "288 KB"},
		{5_300_000, "5.3 MB"},
		{1_500_000_000, "1.5 GB"},
	}
	for _, tt := range tests {
		if got := HumanBytes(tt.n); got != tt.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestTransferNeeds(t *testing.T) {
	const mib, gib = 1 << 20, 1 << 30
	tests := []struct {
		name            string
		files, db, want uint64
	}{
		{"a small copy needs little more than its files", 80 * mib, 1 * mib, 80*mib + 3*mib + minReserve},
		{"a big copy needs no share of the drive", 500 * gib, 100 * mib, 500*gib + 300*mib + minReserve},
		{"nothing to copy still needs the backup and reserve", 0, 100 * mib, 300*mib + minReserve},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TransferNeeds(tt.files, tt.db); got != tt.want {
				t.Errorf("TransferNeeds = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSpaceReportsFreeWithinTotal(t *testing.T) {
	free, total, err := Space(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 || free > total {
		t.Errorf("free %d, total %d: want 0 < free <= total", free, total)
	}
}
