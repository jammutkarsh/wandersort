package utils

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// SHA256File returns the hex-encoded SHA-256 hash of the file at path.
func SHA256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}
