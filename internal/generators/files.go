package generators

import (
	"fmt"
	"os"
	"path/filepath"
)

// Options controls generator file I/O behaviour.
type Options struct {
	DryRun         bool
	Verbose        bool
	ForceBootstrap bool
}

// fileExists reports whether the path exists on disk.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ensureDir creates the directory (and parents) if it does not exist.
func ensureDir(dir string, opts Options) error {
	if opts.DryRun {
		if opts.Verbose {
			fmt.Printf("      (dry-run) mkdir -p %s\n", dir)
		}
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

// writeFile writes data to path. In dry-run mode it prints what would be written.
func writeFile(path string, data []byte, opts Options) error {
	if opts.DryRun {
		fmt.Printf("  (dry-run) write %s (%d bytes)\n", path, len(data))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create dir for %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
