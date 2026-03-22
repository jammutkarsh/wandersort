package util

import (
	"log"
	"os"
)

type Util struct {
	HomeDir string
}

func NewUtil() *Util {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Panic("Failed to get user home directory", "error", err)
	}
	return &Util{HomeDir: home}
}
