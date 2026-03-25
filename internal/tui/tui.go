// Package tui provides shared BubbleTea utilities.
package tui

import (
	"os"

	"github.com/charmbracelet/x/term"
)

// IsInteractive reports whether the current terminal supports TUI interaction.
// Returns false in CI environments, when piped, or when NO_COLOR / --no-interactive is set.
func IsInteractive() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("CI") != "" {
		return false
	}
	return term.IsTerminal(os.Stdout.Fd())
}
