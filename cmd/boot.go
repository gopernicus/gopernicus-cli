package cmd

import (
	"context"
	"fmt"
	"github.com/gopernicus/gopernicus-cli/internal/cli"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/fwsource"
	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/scaffold"
)

var bootCmd = &cli.Command{
	Name:  "boot",
	Short: "Bootstrap project components from reflected schema",
	Long: `Bootstrap project components for one or all domains.

Reads the "domains" mapping under each database in gopernicus.yml (or the
legacy top-level "domains" key when no database declares its own) and
scaffolds the corresponding files. Existing files are never overwritten.`,
	Usage: "gopernicus boot <subcommand>",
}

func init() {
	bootCmd.SubCommands = []*cli.Command{
		{
			Name:  "repos",
			Short: "Bootstrap repos for a domain (or all domains)",
			Long: `Bootstrap repos for every table mapped to a domain in gopernicus.yml.

If a domain name is given, only that domain's tables are scaffolded.
If no domain is given, all domains across all databases are scaffolded.
Tables not mapped to any domain are ignored. Existing repos are skipped.

Examples:
  gopernicus boot repos              # all domains, all databases
  gopernicus boot repos auth         # just the auth domain
  gopernicus boot repos --db analytics  # all domains in analytics db`,
			Usage: "gopernicus boot repos [domain] [--db <name>]",
			Run:   runBootRepos,
		},
	}
	bootCmd.Run = runBoot
	cli.RegisterCommand(bootCmd)
}

func runBoot(_ context.Context, args []string) error {
	return cli.DispatchSub(bootCmd, args)
}

func runBootRepos(_ context.Context, args []string) error {
	dbName := flagValue(args, "--db")

	// Parse optional domain argument (first positional arg).
	domainFilter := firstPositional(args, "--db")

	root, m, err := loadProject()
	if err != nil {
		return err
	}

	// Determine which databases to process.
	var dbNames []string
	if dbName != "" {
		dbNames = []string{dbName}
	} else {
		dbNames = m.DatabaseNames()
	}

	// Resolve framework source once for all tables.
	fwSourceDir, _ := fwsource.ResolveDir() // empty on error; falls back to generic scaffold

	var count int
	for _, db := range dbNames {
		dbConf := m.DatabaseOrDefault(db)
		if dbConf == nil {
			continue
		}
		dbDomains := dbConf.Domains
		if len(dbDomains) == 0 {
			continue
		}

		// Determine which domains to process.
		var domains []string
		if domainFilter != "" {
			if _, ok := dbDomains[domainFilter]; !ok {
				return fmt.Errorf("domain %q not found in database %q\n\nDefined domains: %s",
					domainFilter, db, strings.Join(sortedKeys(dbDomains), ", "))
			}
			domains = []string{domainFilter}
		} else {
			domains = sortedKeys(dbDomains)
		}

		schemaNames := dbConf.SchemasOrDefault()

		for _, domain := range domains {
			tables := dbDomains[domain]
			sort.Strings(tables)

			for _, tableName := range tables {
				// Skip if already scaffolded (queries.sql exists).
				repoDir := generators.RepoDir(domain, tableName, root)
				if fileExists(filepath.Join(repoDir, "queries.sql")) {
					fmt.Printf("  skip %s/%s (already exists)\n", domain, tableName)
					continue
				}

				// Find the table in the reflected schema.
				table, _, err := scaffold.FindTableInSchemas(root, db, schemaNames, tableName)
				if err != nil {
					fmt.Printf("  skip %s/%s (not in reflected schema)\n", domain, tableName)
					continue
				}

				if err := scaffold.RepoForTable(root, domain, table, fwSourceDir); err != nil {
					return err
				}
				if err := scaffold.BridgeYMLForTable(root, domain, table, fwSourceDir); err != nil {
					return err
				}
				count++
			}
		}
	}

	if count == 0 {
		fmt.Println("\n  No new repos to scaffold (all tables already have repos).")
	} else {
		fmt.Printf("\n  Scaffolded %d repos.\n\n", count)
		fmt.Println("Next steps:")
		fmt.Println("  1. Edit queries.sql files to customize operations")
		fmt.Println("  2. Run 'gopernicus generate' to generate code from queries")
	}
	return nil
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
