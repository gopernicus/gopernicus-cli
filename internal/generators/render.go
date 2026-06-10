package generators

import (
	"fmt"
	"go/format"
)

// renderGoFile gofmts rendered Go source and writes it to path. On a format
// failure the raw output is written anyway so it can be inspected, and an
// error naming label (file or context name) is returned.
func renderGoFile(label string, out []byte, path string, opts Options) error {
	formatted, err := format.Source(out)
	if err != nil {
		// Write unformatted for debugging.
		_ = writeFile(path, out, opts)
		return fmt.Errorf("go/format %s: %w\nUnformatted output written for debugging.", label, err)
	}
	if err := writeFile(path, formatted, opts); err != nil {
		return fmt.Errorf("write %s: %w", label, err)
	}
	return nil
}
