package cmd

import (
	"context"
	"fmt"
	"github.com/gopernicus/gopernicus/workshop/codegen/cli"
	"os"
	"strings"

	"github.com/gopernicus/gopernicus/workshop/codegen/fwsource"
	"github.com/gopernicus/gopernicus/workshop/codegen/generators"
	"github.com/gopernicus/gopernicus/workshop/codegen/scaffold"
)

var newCmd = &cli.Command{
	Name:  "new",
	Short: "Scaffold new project components",
	Long:  "Scaffold new project components (repositories, etc.).",
	Usage: "gopernicus new <subcommand>",
}

func init() {
	newCmd.SubCommands = []*cli.Command{
		{
			Name:  "repo",
			Short: "Scaffold a new repository from reflected schema",
			Long: `Scaffold a new repository with queries.sql.

The argument is domain/entity (e.g. "auth/users"). The entity name is used to
look up the table in the reflected schema. Use --table to override the table name.

Creates the repo directory and a queries.sql file with default CRUD operations.
Run 'gopernicus generate' to produce Go code from the queries.

To bootstrap all repos for a domain, use 'gopernicus boot repos <domain>'.

Examples:
  gopernicus new repo auth/users                    # single repo
  gopernicus new repo auth/users --table user_accts # override table name`,
			Usage: "gopernicus new repo <domain/entity> [--db <name>] [--table <name>]",
			Run:   runNewRepo,
		},
		{
			Name:  "case",
			Short: "Scaffold a new use case with bridge",
			Long: `Scaffold a new use case (case) with core logic and HTTP bridge.

Creates both the core case package and its HTTP bridge:
  core/cases/<name>/       — business logic (case.go, errors.go, events.go)
  bridge/cases/<name>bridge/ — HTTP handlers (bridge.go, http.go, model.go)

Case routes register under /cases/<kebab-name>/ to avoid conflicts with
generated CRUD routes.

Examples:
  gopernicus new case tenantadmin     # creates tenantadmin case + bridge
  gopernicus new case audiorecorder   # creates audiorecorder case + bridge`,
			Usage: "gopernicus new case <name>",
			Run:   runNewCase,
		},
	}
	newCmd.Run = runNew
	cli.RegisterCommand(newCmd)
}

func runNew(_ context.Context, args []string) error {
	return cli.DispatchSub(newCmd, args)
}

func runNewRepo(_ context.Context, args []string) error {
	dbName := flagValue(args, "--db")
	if dbName == "" {
		dbName = "primary"
	}
	tableOverride := flagValue(args, "--table")

	entityArg := firstPositional(args, "--db", "--table")

	if entityArg == "" {
		return fmt.Errorf("entity path required: domain/entity (e.g. auth/users)\n\nUsage: gopernicus new repo <domain/entity> [--db <name>] [--table <name>]")
	}

	domainName, entityName := parseDomainEntity(entityArg)
	if domainName == "" {
		return fmt.Errorf("domain is required: use domain/entity (e.g. auth/users)\n\nTo bootstrap all repos for a domain: gopernicus boot repos <domain>")
	}

	tableName := tableOverride
	if tableName == "" {
		tableName = generators.Pluralize(entityName)
	}

	root, m, err := loadProject()
	if err != nil {
		return err
	}

	// Resolve framework source for pre-baked bootstrap files.
	fwSourceDir, _ := fwsource.ResolveDir() // empty on error; falls back to generic scaffold

	// Try to find the table in the reflected schema. If found, scaffold
	// full CRUD queries. If not, scaffold a custom repo with a stub.
	table, _, err := scaffold.FindTable(root, m, dbName, tableName, entityName)
	if err != nil {
		// No matching table — scaffold a custom repo.
		return scaffold.CustomRepo(root, domainName, entityName)
	}

	if err := scaffold.RepoForTable(root, domainName, table, fwSourceDir); err != nil {
		return err
	}

	// Also scaffold bridge.yml for HTTP route configuration.
	if err := scaffold.BridgeYMLForTable(root, domainName, table, fwSourceDir); err != nil {
		return err
	}

	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// parseDomainEntity splits "auth/users" into ("auth", "users").
// If no slash, domain is empty: "widgets" → ("", "widgets").
func parseDomainEntity(arg string) (domain, entity string) {
	parts := strings.SplitN(arg, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", parts[0]
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
