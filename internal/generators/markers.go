package generators

import (
	"fmt"
	"os"
	"strings"
)

const (
	// MarkerStart is the comment that begins a generated block.
	MarkerStart = "// gopernicus:start (DO NOT EDIT between markers)"
	// MarkerEnd is the comment that ends a generated block.
	MarkerEnd = "// gopernicus:end"
)

// ReplaceMarkerBlock replaces the content between gopernicus:start and gopernicus:end
// markers in a file. Content outside the markers is preserved.
// Returns an error if markers are not found.
func ReplaceMarkerBlock(filePath string, newContent string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	result, err := replaceMarkerBlockInSource(string(data), newContent)
	if err != nil {
		return fmt.Errorf("%s: %w", filePath, err)
	}

	return os.WriteFile(filePath, []byte(result), 0644)
}

// replaceMarkerBlockInSource replaces content between markers in a source string.
func replaceMarkerBlockInSource(src, newContent string) (string, error) {
	startIdx := strings.Index(src, MarkerStart)
	if startIdx < 0 {
		return "", fmt.Errorf("marker %q not found", MarkerStart)
	}

	endIdx := strings.Index(src, MarkerEnd)
	if endIdx < 0 {
		return "", fmt.Errorf("marker %q not found", MarkerEnd)
	}

	if endIdx <= startIdx {
		return "", fmt.Errorf("end marker appears before start marker")
	}

	// Build: everything before start marker + start marker + new content + end marker + everything after
	var b strings.Builder
	b.WriteString(src[:startIdx])
	b.WriteString(MarkerStart)
	b.WriteString("\n")
	b.WriteString(newContent)
	if !strings.HasSuffix(newContent, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\t")
	b.WriteString(MarkerEnd)
	b.WriteString(src[endIdx+len(MarkerEnd):])

	return b.String(), nil
}

// HasMarkers returns true if the file contains both start and end markers.
func HasMarkers(filePath string) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, MarkerStart) && strings.Contains(content, MarkerEnd)
}
