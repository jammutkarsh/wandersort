package exiftool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

// extractFirst unwraps the JSON array that exiftool always emits and returns
// the raw bytes of the first (and only) element.
func extractFirst(data []byte) ([]byte, error) {
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("exiftool output is not a JSON array: %w", err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("exiftool returned an empty array")
	}
	return arr[0], nil
}

// parseAndConvert is a generic helper that unmarshals data into T and returns CommonMetadata.
func parseAndConvert[T classifier.Metadata](data []byte) (classifier.CommonMetadata, error) {
	m, err := classifier.ParseFromBytes[T](data)
	if err != nil {
		return classifier.CommonMetadata{}, err
	}
	return m.ToCommon(), nil
}

// dispatchRegistry maps lowercase extensions to their parser function.
// Adding a new format requires only a single line here plus the classifier struct.
var dispatchRegistry = map[string]func([]byte) (classifier.CommonMetadata, error){
	".jpg":  parseAndConvert[classifier.Jpg],
	".jpeg": parseAndConvert[classifier.Jpeg],
	".heic": parseAndConvert[classifier.Heic],
	".png":  parseAndConvert[classifier.Png],
	".bmp":  parseAndConvert[classifier.Bmp],
	".webp": parseAndConvert[classifier.Webp],
	".cr2":  parseAndConvert[classifier.Cr2],
	".dng":  parseAndConvert[classifier.Dng],
	".mp4":  parseAndConvert[classifier.Mp4],
	".mov":  parseAndConvert[classifier.Mov],
	".aae":  parseAndConvert[classifier.Aae],
}

// dispatch parses raw exiftool JSON into the concrete type that matches ext,
// then converts it to CommonMetadata via ToCommon().
func dispatch(ext string, data []byte) (classifier.CommonMetadata, error) {
	fn, ok := dispatchRegistry[strings.ToLower(ext)]
	if !ok {
		return classifier.CommonMetadata{}, fmt.Errorf("unsupported extension: %q", ext)
	}
	return fn(data)
}
