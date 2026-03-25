package cmd

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

// Version is set at build time via -ldflags, falls back to VCS info.
var Version = "dev"

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		Version = info.Main.Version
		return
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 12 {
			Version = "dev+" + s.Value[:12]
			break
		}
	}
}

// Command is a CLI command or subcommand.
type Command struct {
	Name        string
	Short       string // one-line summary shown in help lists
	Long        string // full help text shown on --help
	Usage       string // usage line, e.g. "gopernicus add <component> [@version]"
	Run         func(ctx context.Context, args []string) error
	SubCommands []*Command
}

var (
	commands []*Command
	cmdMap   = make(map[string]*Command)
)

// RegisterCommand registers a top-level command. Called from init() in each command file.
func RegisterCommand(c *Command) {
	commands = append(commands, c)
	cmdMap[c.Name] = c
}

// Execute is the main entry point. Parses os.Args and dispatches.
func Execute(ctx context.Context) error {
	args := os.Args[1:]

	if len(args) == 0 {
		printRootHelp()
		return nil
	}

	// Strip global flags before command dispatch.
	var filtered []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-v", "--version":
			if len(filtered) == 0 {
				fmt.Printf("gopernicus %s\n", Version)
				return nil
			}
			filtered = append(filtered, args[i])
		default:
			filtered = append(filtered, args[i])
		}
	}
	args = filtered

	name := args[0]
	if name == "help" || name == "-h" || name == "--help" {
		printRootHelp()
		return nil
	}

	c, ok := cmdMap[name]
	if !ok {
		return fmt.Errorf("unknown command %q\n\nRun 'gopernicus help' for usage.", name)
	}

	rest := args[1:]
	for _, a := range rest {
		if a == "-h" || a == "--help" || a == "help" {
			printCommandHelp(c)
			return nil
		}
	}

	return c.Run(ctx, rest)
}

func printRootHelp() {
	fmt.Print(`gopernicus — a Go web framework toolkit

Usage:
  gopernicus <command> [flags]

Commands:
`)
	maxLen := 0
	for _, c := range commands {
		if len(c.Name) > maxLen {
			maxLen = len(c.Name)
		}
	}
	for _, c := range commands {
		padding := strings.Repeat(" ", maxLen-len(c.Name))
		fmt.Printf("  %s%s   %s\n", c.Name, padding, c.Short)
	}
	fmt.Print(`
Flags:
  -h, --help      Show help for a command
  -v, --version   Show version

Run 'gopernicus <command> --help' for more information about a command.
`)
}

func printCommandHelp(c *Command) {
	if c.Long != "" {
		fmt.Println(c.Long)
	} else {
		fmt.Println(c.Short)
	}
	fmt.Println()
	if c.Usage != "" {
		fmt.Println("Usage:")
		fmt.Printf("  %s\n", c.Usage)
		fmt.Println()
	}
	if len(c.SubCommands) > 0 {
		fmt.Println("Subcommands:")
		for _, sub := range c.SubCommands {
			fmt.Printf("  %-12s %s\n", sub.Name, sub.Short)
		}
		fmt.Println()
	}
}
