package cmd

import (
	"context"
	"fmt"

	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/project"
)

func init() {
	RegisterCommand(&Command{
		Name:  "generate",
		Short: "Run code generators from queries.sql files",
		Long: `Run code generators from queries.sql files.

Scans core/repositories/ for queries.sql files and generates Go code by
cross-referencing with reflected schema (from 'gopernicus db reflect').

Regenerated files (generated.go) are always overwritten.
Bootstrapped files (model.go, repository.go, store.go) are created once
and never overwritten — they belong to you.

Examples:
  gopernicus generate                    # regenerate all repositories
  gopernicus generate auth               # regenerate only the auth domain
  gopernicus generate --dry-run          # preview what would change
  gopernicus generate --force-bootstrap  # overwrite bootstrap files too`,
		Usage: "gopernicus generate [domain] [--dry-run] [--verbose] [--force-bootstrap]",
		Run:   runGenerate,
	})
}

func runGenerate(_ context.Context, args []string) error {
	root, err := project.MustFindRoot()
	if err != nil {
		return err
	}

	m, err := manifest.Load(root)
	if err != nil {
		return err
	}

	domain := ""
	dryRun := false
	verbose := false
	forceBootstrap := false
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		case "--verbose", "-v":
			verbose = true
		case "--force-bootstrap", "-f":
			forceBootstrap = true
		default:
			if len(a) > 0 && a[0] != '-' {
				domain = a
			}
		}
	}

	cfg := generators.Config{
		ProjectRoot:    root,
		Manifest:       m,
		Domain:         domain,
		DryRun:         dryRun,
		Verbose:        verbose,
		ForceBootstrap: forceBootstrap,
	}

	if err := generators.Run(cfg); err != nil {
		return err
	}

	fmt.Println()
	if dryRun {
		fmt.Println("  (dry run — no files written)")
	} else {
		fmt.Println("  ✓ generation complete")
	}
	return nil
}
