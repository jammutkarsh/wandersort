package exiftool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jammutkarsh/wandersort/pkg/core/classifier"
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

// dispatch parses raw exiftool JSON into the concrete type that matches ext,
// then converts it to CommonMetadata via ToCommon().
func dispatch(ext string, data []byte) (classifier.CommonMetadata, error) {
	switch strings.ToLower(ext) {
	case ".jpg":
		m, err := classifier.ParseFromBytes[classifier.Jpg](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	case ".jpeg":
		m, err := classifier.ParseFromBytes[classifier.Jpeg](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	case ".heic":
		m, err := classifier.ParseFromBytes[classifier.Heic](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	case ".png":
		m, err := classifier.ParseFromBytes[classifier.Png](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	case ".bmp":
		m, err := classifier.ParseFromBytes[classifier.Bmp](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	case ".webp":
		m, err := classifier.ParseFromBytes[classifier.Webp](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	case ".cr2":
		m, err := classifier.ParseFromBytes[classifier.Cr2](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	case ".dng":
		m, err := classifier.ParseFromBytes[classifier.Dng](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	case ".mp4":
		m, err := classifier.ParseFromBytes[classifier.Mp4](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	case ".mov":
		m, err := classifier.ParseFromBytes[classifier.Mov](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	case ".aae":
		m, err := classifier.ParseFromBytes[classifier.Aae](data)
		if err != nil {
			return classifier.CommonMetadata{}, err
		}
		return m.ToCommon(), nil
	default:
		return classifier.CommonMetadata{}, fmt.Errorf("unsupported extension: %q", ext)
	}
}
