package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gopernicus/gopernicus/workshop/codegen/cli"
	"github.com/gopernicus/gopernicus/workshop/codegen/generators"
	"github.com/gopernicus/gopernicus/workshop/codegen/project"
)

// toolImportPath is the go.mod tool directive target init pins into every
// scaffolded project.
const toolImportPath = generators.FrameworkModulePath + "/workshop/gopernicus"

// launcherCommands delegate to the project's pinned generator via
// `go tool gopernicus`, so the code generator that runs is always the one
// shipped with the framework version the project links.
var launcherCommands = []struct {
	name  string
	short string
	usage string
}{
	{"generate", "Run code generators from queries.sql files", "gopernicus generate [domain] [--dry-run] [--verbose] [--force-bootstrap]"},
	{"db", "Database utilities (migrate, reflect, status)", "gopernicus db <subcommand>"},
	{"new", "Scaffold project components", "gopernicus new <subcommand>"},
	{"boot", "Bootstrap project components from reflected schema", "gopernicus boot <subcommand>"},
	{"doctor", "Check project health", "gopernicus doctor"},
}

func init() {
	for _, lc := range launcherCommands {
		name := lc.name
		cli.RegisterCommand(&cli.Command{
			Name:  name,
			Short: lc.short,
			Long: lc.short + ".\n\n" +
				"Delegates to the project's pinned generator (`go tool gopernicus " + name + "`),\n" +
				"so the tool version always matches the framework version in go.mod.\n" +
				"Run inside a project for full help: go tool gopernicus " + name + " --help",
			Usage: lc.usage,
			Run: func(ctx context.Context, args []string) error {
				return launchTool(ctx, name, args)
			},
		})
	}
}

// launchTool execs the project's pinned tool, passing stdio through. Projects
// scaffolded before the tool directive existed get an upgrade hint instead of
// the opaque `go tool` failure.
func launchTool(ctx context.Context, name string, args []string) error {
	root, err := project.MustFindRoot()
	if err != nil {
		return err
	}

	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("reading go.mod: %w", err)
	}
	if !strings.Contains(string(gomod), toolImportPath) {
		return fmt.Errorf(
			"this project does not pin the gopernicus tool in go.mod\n\n"+
				"Pin it (requires a gopernicus version that ships the tool):\n"+
				"  go get %s@latest\n"+
				"  go mod edit -tool=%s\n\n"+
				"Then re-run this command.",
			generators.FrameworkModulePath, toolImportPath,
		)
	}

	tool := exec.CommandContext(ctx, "go", append([]string{"tool", "gopernicus", name}, args...)...)
	tool.Stdin = os.Stdin
	tool.Stdout = os.Stdout
	tool.Stderr = os.Stderr
	if err := tool.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// The tool already printed its error; exit quietly with context.
			return fmt.Errorf("gopernicus %s failed", name)
		}
		return fmt.Errorf("running go tool gopernicus: %w", err)
	}
	return nil
}
