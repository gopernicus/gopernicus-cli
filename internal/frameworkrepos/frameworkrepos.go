// Package frameworkrepos embeds the canonical bootstrap files for all
// framework-managed tables (auth + rebac domains). When `gopernicus boot repos`
// encounters a known framework table it uses these files instead of generating
// a generic scaffold, so users start with the correct custom queries, permissions,
// auth annotations, and store implementations already in place.
//
// To sync with the framework source:
//
//	make sync-framework-repos
package frameworkrepos

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

//go:embed auth rebac tenancy events
var embedded embed.FS

// Files returns all bootstrap files for a known framework table as a map of
// relative path → content. Paths are relative to the entity directory, e.g.:
//
//	"queries.sql"
//	"repository.go"
//	"rebacrelationshipspgx/store.go"
//
// Returns an empty map when the table is not a known framework table.
func Files(domain, tableName string) map[string][]byte {
	prefix := domain + "/" + tableName
	result := make(map[string][]byte)

	err := fs.WalkDir(embedded, prefix, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := embedded.ReadFile(p)
		if err != nil {
			return err
		}
		// Store with path relative to the entity root, stripping .tmpl suffix.
		rel := strings.TrimSuffix(p[len(prefix)+1:], ".tmpl")
		result[rel] = data
		return nil
	})
	if err != nil {
		return nil
	}
	return result
}

// QueriesSQL is a convenience wrapper that returns just the queries.sql content
// for a framework table, or (nil, false) if not found.
func QueriesSQL(domain, tableName string) ([]byte, bool) {
	data, err := embedded.ReadFile(path.Join(domain, tableName, "queries.sql"))
	if err != nil {
		return nil, false
	}
	return data, true
}
