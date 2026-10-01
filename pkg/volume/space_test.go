package volume

import "testing"

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

func TestTransferNeeds(t *testing.T) {
	const gib = 1 << 30
	tests := []struct {
		name                   string
		files, db, total, want uint64
	}{
		{"small volume keeps the 1 GiB floor", 10 * gib, 1 << 20, 50 * gib, 10*gib + 2<<20 + gib},
		{"large volume keeps 1%", 10 * gib, 0, 1000 * gib, 10*gib + 10*gib},
		{"nothing to copy still needs the backup and reserve", 0, 100 << 20, 0, 200<<20 + gib},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TransferNeeds(tt.files, tt.db, tt.total); got != tt.want {
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
