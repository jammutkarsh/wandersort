package volume

import (
	"runtime"
	"testing"
)

func TestLabel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	tests := []struct{ path, want string }{
		{"/Volumes/SD_CARD/DCIM/IMG_1.JPG", "SD_CARD"},
		{"/Volumes/Backup Drive/a.jpg", "Backup Drive"},
		{"/media/<user>/CARD/a.jpg", "CARD"},
		{"/run/media/<user>/CARD/a.jpg", "CARD"},
		{"/mnt/nas/photos/a.jpg", "nas"},
		{"/media/<user>", ThisComputer},
		{"/home/<user>/Pictures/a.jpg", ThisComputer},
	}
	for _, tt := range tests {
		if got := Label(tt.path); got != tt.want {
			t.Errorf("Label(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
