package volume

import "fmt"

// FreeBytes returns the bytes available to the current user on the volume
// containing path.
func FreeBytes(path string) (uint64, error) {
	free, _, err := Space(path)
	return free, err
}

// minReserve and reserveShare size the room a transfer leaves free: 1 GiB or 1%
// of the volume, whichever is more. A full disk breaks the database's next
// write.
const (
	minReserve   = 1 << 30
	reserveShare = 100 // 1/100 of the volume
)

// TransferNeeds is the free space a transfer of fileBytes needs: the files, the
// backup (2 × dbBytes: plain and compressed copies briefly coexist) and the
// reserve.
func TransferNeeds(fileBytes, dbBytes, total uint64) uint64 {
	return fileBytes + 2*dbBytes + max(minReserve, total/reserveShare)
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
