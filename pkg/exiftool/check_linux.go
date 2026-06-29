//go:build linux

package exiftool

func installInstructions() string {
	return "Install exiftool: sudo apt-get install libimage-exiftool-perl"
}