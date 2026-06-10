// Package scaffold implements the repo-scaffolding engine: it writes
// queries.sql and bridge.yml files for entities from reflected schema info.
// Inputs are the project root, schema info, framework source dir, and names;
// flag parsing, manifest loading, and TUI concerns stay with callers.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gopernicus/gopernicus-cli/internal/fwsource"
	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// FindTable looks up a table in the reflected schema across all configured schemas.
func FindTable(root string, m *manifest.Manifest, dbName, tableName, entityName string) (*schema.TableInfo, string, error) {
	dbConf := m.DatabaseOrDefault(dbName)
	schemaNames := []string{"public"}
	if dbConf != nil {
		schemaNames = dbConf.SchemasOrDefault()
	}

	// Try table name first, then entity name as fallback.
	for _, tryName := range []string{tableName, entityName} {
		for _, s := range schemaNames {
			jsonPath := filepath.Join(root, manifest.MigrationsDir(dbName), "_"+s+".json")
			rs, err := schema.LoadJSON(jsonPath)
			if err != nil {
				continue
			}
			if t, ok := rs.Tables[tryName]; ok {
				return t, s, nil
			}
		}
	}

	return nil, "", fmt.Errorf(
		"table %q not found in reflected schema\n\n"+
			"Run 'gopernicus db reflect' first, or use --table to specify the table name.",
		tableName,
	)
}

// FindTableInSchemas looks up a table across the given schemas for a database.
func FindTableInSchemas(root, dbName string, schemaNames []string, tableName string) (*schema.TableInfo, string, error) {
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

// RepoForTable creates the repo directory and a queries.sql file
// with default CRUD operations derived from the reflected table schema.
// Go code (model.go, repository.go, store.go) is created by `gopernicus generate`.
func RepoForTable(root, domainName string, table *schema.TableInfo, fwSourceDir string) error {
	tableName := table.TableName
	entitySingular := generators.Singularize(tableName)
	anc := DetectAncestry(table)

	repoDir := generators.RepoDir(domainName, tableName, root)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", repoDir, err)
	}

	// If this is a known framework table, use pre-baked files from the
	// framework source (includes custom queries, store methods, repository methods).
	// Otherwise scaffold a generic CRUD queries.sql.
	if repoFiles := fwsource.RepoFiles(fwSourceDir, domainName, tableName); len(repoFiles) > 0 {
		for relPath, content := range repoFiles {
			dest := filepath.Join(repoDir, filepath.FromSlash(relPath))
			if fileExists(dest) {
				fmt.Printf("  skip  %s/%s/%s (already exists)\n", domainName, tableName, relPath)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return fmt.Errorf("creating dir for %s: %w", relPath, err)
			}
			if err := os.WriteFile(dest, content, 0644); err != nil {
				return fmt.Errorf("writing %s: %w", relPath, err)
			}
			fmt.Printf("  create %s/%s/%s\n", domainName, tableName, relPath)
		}
	} else {
		queriesPath := filepath.Join(repoDir, "queries.sql")
		if fileExists(queriesPath) {
			fmt.Printf("  skip  %s/%s/queries.sql (already exists)\n", domainName, tableName)
		} else {
			content := Queries(table, tableName, entitySingular, anc)
			if err := os.WriteFile(queriesPath, []byte(content), 0644); err != nil {
				return fmt.Errorf("writing queries.sql: %w", err)
			}
			fmt.Printf("  create %s/%s/queries.sql\n", domainName, tableName)
		}
	}

	return nil
}

// CustomRepo creates a repo directory with a stub queries.sql
// for custom/joined queries that don't map to a single reflected table.
func CustomRepo(root, domainName, entityName string) error {
	repoDir := generators.RepoDir(domainName, entityName, root)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", repoDir, err)
	}

	queriesPath := filepath.Join(repoDir, "queries.sql")
	if fileExists(queriesPath) {
		fmt.Printf("  skip %s/%s (queries.sql already exists)\n", domainName, entityName)
		return nil
	}

	content := fmt.Sprintf(`-- Custom queries for %s.
-- No reflected table found — write your queries here.

-- List %s
-- SELECT ... FROM ... ;

`, entityName, generators.ToPascalCase(generators.Pluralize(entityName)))
	if err := os.WriteFile(queriesPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing queries.sql: %w", err)
	}
	fmt.Printf("  create %s/queries.sql (custom)\n", repoDir)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
