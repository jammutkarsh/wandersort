package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var roots = []string{
	"/Users/utc/Pictures/Pictures",
	"/Users/utc/Downloads/Backup",
}

var allowedExts = map[string]struct{}{
	"aae":  {},
	"bmp":  {},
	"cr2":  {},
	"dng":  {},
	"heic": {},
	"jpeg": {},
	"jpg":  {},
	"mov":  {},
	"mp4":  {},
	"png":  {},
	"webp": {},
}

const chunkSize = 400

func main() {
	filesByExt, err := findMediaFilesByExt(roots)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scan error:", err)
		os.Exit(1)
	}

	for ext, files := range filesByExt {
		if len(files) == 0 {
			continue
		}

		fmt.Fprintf(os.Stderr, "Processing .%s (%d files)\n", ext, len(files))

		merged := make(map[string]any, 512)

		for i := 0; i < len(files); i += chunkSize {
			end := min(i+chunkSize, len(files))
			chunk := files[i:end]

			records, err := runExifTool(chunk)
			if err != nil {
				fmt.Fprintln(os.Stderr, "exiftool error:", err)
				os.Exit(1)
			}

			for _, rec := range records {
				for k, v := range rec {
					merged[k] = v // last-write-wins
				}
			}
		}

		name := ext + ".json"
		f, err := os.Create(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "create file error:", err)
			os.Exit(1)
		}

		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		if err := enc.Encode(merged); err != nil {
			fmt.Fprintln(os.Stderr, "json encode error:", err)
			f.Close()
			os.Exit(1)
		}
		f.Close()

		fmt.Fprintf(os.Stderr, "Wrote %s (%d fields)\n", name, len(merged))
	}
}

func findMediaFilesByExt(roots []string) (map[string][]string, error) {
	out := make(map[string][]string)

	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(d.Name()), "."))
			if _, ok := allowedExts[ext]; ok {
				out[ext] = append(out[ext], path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

func runExifTool(paths []string) ([]map[string]any, error) {
	args := []string{"-json", "-n"}
	args = append(args, paths...)

	cmd := exec.Command("exiftool", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("exiftool failed: %w: %s", err, stderr.String())
	}

	var records []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &records); err != nil {
		return nil, fmt.Errorf("invalid exiftool json: %w", err)
	}

	return records, nil
}
