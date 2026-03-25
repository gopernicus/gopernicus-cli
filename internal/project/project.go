// Package project provides utilities for detecting and inspecting a gopernicus project.
package project

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FindRoot walks up from the current directory to find the project root,
// identified by the presence of a go.mod file.
func FindRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find project root: no go.mod found in current or parent directories")
		}
		dir = parent
	}
}

// MustFindRoot is like FindRoot but returns a user-friendly error.
func MustFindRoot() (string, error) {
	root, err := FindRoot()
	if err != nil {
		return "", fmt.Errorf("%w\n\nAre you inside a Go project? Run 'gopernicus init' to create one.", err)
	}
	return root, nil
}

// ModulePath reads the Go module path from the go.mod file at the project root.
func ModulePath(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("reading go.mod: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module")), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scanning go.mod: %w", err)
	}
	return "", fmt.Errorf("module directive not found in go.mod")
}
