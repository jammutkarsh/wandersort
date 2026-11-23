package volume

import "fmt"

// FreeBytes returns the bytes available to the current user on the volume
// containing path.
func FreeBytes(path string) (uint64, error) {
	free, _, err := Space(path)
	return free, err
}

// minReserve is the room a transfer leaves free past its files, the backup
// and the database's own writes: a full disk breaks the database's next write.
const minReserve = 64 << 20

// TransferNeeds is the free space a transfer of fileBytes needs: the files, the
// backup (2 × dbBytes: plain and compressed copies briefly coexist), the
// database's write-ahead log (up to dbBytes again) and the reserve. It grows
// with what is written, never with the drive's size.
func TransferNeeds(fileBytes, dbBytes uint64) uint64 {
	return fileBytes + 3*dbBytes + minReserve
}

// HumanBytes renders n as a short decimal size: "1.5 GB", "288 KB".
func HumanBytes(n uint64) string {
	const unit = 1000 // the units Finder and Explorer show
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	if v >= 10 {
		return fmt.Sprintf("%.0f %cB", v, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f %cB", v, "KMGTPE"[exp])
}
