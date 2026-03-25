package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/project"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

var bootCmd = &Command{
	Name:  "boot",
	Short: "Bootstrap project components from reflected schema",
	Long: `Bootstrap project components for one or all domains.

Reads the "domains" mapping under each database in gopernicus.yml and
scaffolds the corresponding files. Existing files are never overwritten.`,
	Usage: "gopernicus boot <subcommand>",
}

func init() {
	bootCmd.SubCommands = []*Command{
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
	RegisterCommand(bootCmd)
}

func runBoot(_ context.Context, args []string) error {
	if len(args) == 0 {
		printCommandHelp(bootCmd)
		return nil
	}
	name := args[0]
	for _, sub := range bootCmd.SubCommands {
		if sub.Name == name {
			rest := args[1:]
			for _, a := range rest {
				if a == "-h" || a == "--help" {
					printCommandHelp(sub)
					return nil
				}
			}
			return sub.Run(context.Background(), rest)
		}
	}
	return fmt.Errorf("unknown boot subcommand %q\n\nRun 'gopernicus boot --help' for usage.", name)
}

func runBootRepos(_ context.Context, args []string) error {
	dbName := flagValue(args, "--db")

	// Parse optional domain argument (first positional arg).
	var domainFilter string
	for i := 0; i < len(args); i++ {
		if args[i] == "--db" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--db=") {
			continue
		}
		if !strings.HasPrefix(args[i], "-") && domainFilter == "" {
			domainFilter = args[i]
		}
	}

	root, err := project.MustFindRoot()
	if err != nil {
		return err
	}
	m, err := manifest.Load(root)
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

	var count int
	for _, db := range dbNames {
		dbConf := m.DatabaseOrDefault(db)
		if dbConf == nil {
			continue
		}
		if len(dbConf.Domains) == 0 {
			continue
		}

		// Determine which domains to process.
		var domains []string
		if domainFilter != "" {
			if _, ok := dbConf.Domains[domainFilter]; !ok {
				return fmt.Errorf("domain %q not found in database %q\n\nDefined domains: %s",
					domainFilter, db, strings.Join(sortedKeys(dbConf.Domains), ", "))
			}
			domains = []string{domainFilter}
		} else {
			domains = sortedKeys(dbConf.Domains)
		}

		schemaNames := dbConf.SchemasOrDefault()

		for _, domain := range domains {
			tables := dbConf.Domains[domain]
			sort.Strings(tables)

			for _, tableName := range tables {
				// Skip if already scaffolded (queries.sql exists).
				repoDir := generators.RepoDir(domain, tableName, root)
				if fileExists(filepath.Join(repoDir, "queries.sql")) {
					fmt.Printf("  skip %s/%s (already exists)\n", domain, tableName)
					continue
				}

				// Find the table in the reflected schema.
				table, _, err := findTableInSchemas(root, db, schemaNames, tableName)
				if err != nil {
					fmt.Printf("  skip %s/%s (not in reflected schema)\n", domain, tableName)
					continue
				}

				if err := scaffoldRepoForTable(root, db, domain, table); err != nil {
					return err
				}
				if err := scaffoldBridgeYMLForTable(root, domain, table); err != nil {
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

// findTableInSchemas looks up a table across the given schemas for a database.
func findTableInSchemas(root, dbName string, schemaNames []string, tableName string) (*schema.TableInfo, string, error) {
	for _, s := range schemaNames {
		jsonPath := filepath.Join(root, manifest.MigrationsDir(dbName), "_"+s+".json")
		rs, err := schema.LoadJSON(jsonPath)
		if err != nil {
			continue
		}
		if t, ok := rs.Tables[tableName]; ok {
			return t, s, nil
		}
	}
	return nil, "", fmt.Errorf("table %q not found in reflected schema", tableName)
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
