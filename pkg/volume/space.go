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

// HumanBytes renders n as a short base-1024 size, e.g. "1.5 GiB"
func HumanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
