package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// nestedOutboxQueries is a minimal event_outbox repository for nested-shape
// dispatch tests. No @database: annotation — the manifest is the binding
// source under the nested shape.
const nestedOutboxQueries = `-- @func: Get
SELECT *
FROM event_outbox
WHERE event_id = @event_id
;

-- @func: Delete
DELETE FROM event_outbox
WHERE event_id = @event_id
;
`

func nestedOutboxSchema() *schema.ReflectedSchema {
	return &schema.ReflectedSchema{
		SchemaName: "public",
		Tables: map[string]*schema.TableInfo{
			"event_outbox": {
				TableName: "event_outbox",
				Schema:    "public",
				PrimaryKey: &schema.PrimaryKeyInfo{
					Column: "event_id",
					DBType: "text",
					GoType: "string",
				},
				Columns: []schema.ColumnInfo{
					{Name: "event_id", DBType: "text", GoType: "string", IsPrimaryKey: true},
					{Name: "event_type", DBType: "text", GoType: "string"},
					{Name: "payload", DBType: "text", GoType: "string"},
					{Name: "created_at", DBType: "timestamptz", GoType: "time.Time", GoImport: "time"},
				},
			},
		},
	}
}

// writeNestedProject lays out the events/eventoutbox repo and returns the
// project root and the repo directory.
func writeNestedProject(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	repoDir := filepath.Join(root, "core", "repositories", "events", "eventoutbox")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "queries.sql"), []byte(nestedOutboxQueries), 0644); err != nil {
		t.Fatalf("write queries.sql: %v", err)
	}
	return root, repoDir
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// TestRunNested_SharedEntityAcrossModes covers an entity hosted by a pgx-mode
// primary and a spec-mode sqlite database: both store packages generate, the
// entity package generates once from primary's (canonical) snapshot, and the
// events domain gets one composite per database with db-qualified cache key
// prefixes.
func TestRunNested_SharedEntityAcrossModes(t *testing.T) {
	root, repoDir := writeNestedProject(t)

	m := &manifest.Manifest{
		Databases: map[string]*manifest.DatabaseConfig{
			"primary": {
				Driver:    manifest.DriverPostgres,
				URLEnvVar: "APP_DB_URL",
				Domains:   map[string][]string{"events": {"event_outbox"}},
			},
			"otherdb": {
				Driver:  manifest.DriverSQLite,
				Domains: map[string][]string{"events": {"event_outbox"}},
			},
		},
	}

	// Only the canonical database's snapshot exists — the otherdb snapshot
	// must not be required.
	schemas := map[string]*schema.ReflectedSchema{"primary:public": nestedOutboxSchema()}

	cfg := Config{ProjectRoot: root, Manifest: m}
	if err := runNested(cfg, schemas, "github.com/example/app", Options{}); err != nil {
		t.Fatalf("runNested: %v", err)
	}

	// Both store packages: pgx (primary) and spec (otherdb) siblings.
	for _, want := range []string{
		filepath.Join(repoDir, "eventoutboxpgx", "generated.go"),
		filepath.Join(repoDir, "eventoutboxstore", "generated.go"),
		filepath.Join(repoDir, "generated.go"), // entity package, once
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected generated file %s: %v", want, err)
		}
	}

	// Multi-homed entity: cache exposes the prefix-qualified constructor.
	cacheFile := mustReadFile(t, filepath.Join(repoDir, "generated_cache.go"))
	if !strings.Contains(cacheFile, "func NewCacheStoreWithKeyPrefix(") {
		t.Errorf("generated_cache.go missing NewCacheStoreWithKeyPrefix for multi-homed entity")
	}

	domainDir := filepath.Join(root, "core", "repositories", "events")

	shared := mustReadFile(t, filepath.Join(domainDir, "generated_composite.go"))
	if !strings.Contains(shared, "type Repositories struct {") {
		t.Errorf("shared composite missing Repositories struct\n--- output ---\n%s", shared)
	}
	if !strings.Contains(shared, "type TxRunner func(") {
		t.Errorf("shared composite missing TxRunner (otherdb is spec mode)\n--- output ---\n%s", shared)
	}
	if strings.Contains(shared, "func NewRepositories") {
		t.Errorf("shared composite must not declare constructors\n--- output ---\n%s", shared)
	}

	pgxComposite := mustReadFile(t, filepath.Join(domainDir, "generated_composite_primary.go"))
	for _, want := range []string{
		"func NewRepositoriesPrimary(log *slog.Logger, db pgxdb.Querier, c *cache.Cache, bus gopevents.Bus) *Repositories {",
		`eventoutbox.NewCacheStoreWithKeyPrefix(eventoutboxpgx.NewStore(log, db), c, "primary:events:event_outbox")`,
	} {
		if !strings.Contains(pgxComposite, want) {
			t.Errorf("generated_composite_primary.go missing fragment %q\n--- output ---\n%s", want, pgxComposite)
		}
	}

	specComposite := mustReadFile(t, filepath.Join(domainDir, "generated_composite_otherdb.go"))
	for _, want := range []string{
		"func NewRepositoriesOtherdb(log *slog.Logger, q crud.Querier, d crud.Dialect, inTx TxRunner, c *cache.Cache, bus gopevents.Bus) (*Repositories, error) {",
		`eventoutbox.NewCacheStoreWithKeyPrefix(eventoutboxStore, c, "otherdb:events:event_outbox")`,
	} {
		if !strings.Contains(specComposite, want) {
			t.Errorf("generated_composite_otherdb.go missing fragment %q\n--- output ---\n%s", want, specComposite)
		}
	}
}

// TestRunNested_DedupeSameModeStores covers two spec-mode databases sharing
// an entity: exactly one <entity>store package generates (no pgx package),
// while each database still gets its own composite.
func TestRunNested_DedupeSameModeStores(t *testing.T) {
	root, repoDir := writeNestedProject(t)

	m := &manifest.Manifest{
		Databases: map[string]*manifest.DatabaseConfig{
			"alpha": {Driver: manifest.DriverSQLite, Domains: map[string][]string{"events": {"event_outbox"}}},
			"beta":  {Driver: manifest.DriverSQLite, Domains: map[string][]string{"events": {"event_outbox"}}},
		},
	}
	// No "primary": canonical order is sorted, so alpha is canonical.
	schemas := map[string]*schema.ReflectedSchema{"alpha:public": nestedOutboxSchema()}

	cfg := Config{ProjectRoot: root, Manifest: m}
	if err := runNested(cfg, schemas, "github.com/example/app", Options{}); err != nil {
		t.Fatalf("runNested: %v", err)
	}

	if _, err := os.Stat(filepath.Join(repoDir, "eventoutboxstore", "generated.go")); err != nil {
		t.Errorf("expected single spec store package: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "eventoutboxpgx")); !os.IsNotExist(err) {
		t.Errorf("pgx store package must not exist when every hosting database is spec mode")
	}

	domainDir := filepath.Join(root, "core", "repositories", "events")
	for db, ctor := range map[string]string{"alpha": "NewRepositoriesAlpha", "beta": "NewRepositoriesBeta"} {
		composite := mustReadFile(t, filepath.Join(domainDir, "generated_composite_"+db+".go"))
		if !strings.Contains(composite, "func "+ctor+"(") {
			t.Errorf("generated_composite_%s.go missing %s\n--- output ---\n%s", db, ctor, composite)
		}
		if !strings.Contains(composite, `"`+db+`:events:event_outbox"`) {
			t.Errorf("generated_composite_%s.go missing db-qualified cache prefix", db)
		}
		if !strings.Contains(composite, "eventoutboxstore.NewStore(") {
			t.Errorf("generated_composite_%s.go must wire the shared spec store package", db)
		}
	}
}

// TestRunNested_SingleDatabaseKeepsClassicOutput pins that a single-hosted
// domain under the nested shape keeps today's file name, constructor, and
// default cache prefix (no per-db files, no prefix constructor).
func TestRunNested_SingleDatabaseKeepsClassicOutput(t *testing.T) {
	root, repoDir := writeNestedProject(t)

	m := &manifest.Manifest{
		Databases: map[string]*manifest.DatabaseConfig{
			"primary": {
				Driver:    manifest.DriverPostgres,
				URLEnvVar: "APP_DB_URL",
				Domains:   map[string][]string{"events": {"event_outbox"}},
			},
		},
	}
	schemas := map[string]*schema.ReflectedSchema{"primary:public": nestedOutboxSchema()}

	cfg := Config{ProjectRoot: root, Manifest: m}
	if err := runNested(cfg, schemas, "github.com/example/app", Options{}); err != nil {
		t.Fatalf("runNested: %v", err)
	}

	domainDir := filepath.Join(root, "core", "repositories", "events")
	composite := mustReadFile(t, filepath.Join(domainDir, "generated_composite.go"))
	if !strings.Contains(composite, "func NewRepositories(log *slog.Logger, db pgxdb.Querier, c *cache.Cache, bus gopevents.Bus) *Repositories {") {
		t.Errorf("single-database composite changed shape\n--- output ---\n%s", composite)
	}
	if _, err := os.Stat(filepath.Join(domainDir, "generated_composite_primary.go")); !os.IsNotExist(err) {
		t.Errorf("single-database domain must not emit per-db composite files")
	}

	cacheFile := mustReadFile(t, filepath.Join(repoDir, "generated_cache.go"))
	if strings.Contains(cacheFile, "NewCacheStoreWithKeyPrefix") {
		t.Errorf("single-homed entity cache must keep the current shape (no NewCacheStoreWithKeyPrefix)")
	}
	if !strings.Contains(cacheFile, `KeyPrefix: "events:event_outbox"`) {
		t.Errorf("single-homed entity must keep the default cache key prefix")
	}
}

// TestRunNested_MissingQueriesFile verifies the clear error when the manifest
// declares an entity that was never scaffolded.
func TestRunNested_MissingQueriesFile(t *testing.T) {
	root := t.TempDir()

	m := &manifest.Manifest{
		Databases: map[string]*manifest.DatabaseConfig{
			"primary": {
				Driver:    manifest.DriverPostgres,
				URLEnvVar: "APP_DB_URL",
				Domains:   map[string][]string{"events": {"event_outbox"}},
			},
		},
	}
	schemas := map[string]*schema.ReflectedSchema{"primary:public": nestedOutboxSchema()}

	cfg := Config{ProjectRoot: root, Manifest: m}
	err := runNested(cfg, schemas, "github.com/example/app", Options{})
	if err == nil {
		t.Fatal("expected missing queries.sql error, got nil")
	}
	if !strings.Contains(err.Error(), "gopernicus new repo events/event_outbox") {
		t.Errorf("error %q must suggest 'gopernicus new repo events/event_outbox'", err)
	}
}

// TestRunNested_UnknownDomainFilter verifies the domain filter is validated
// against the manifest.
func TestRunNested_UnknownDomainFilter(t *testing.T) {
	root, _ := writeNestedProject(t)

	m := &manifest.Manifest{
		Databases: map[string]*manifest.DatabaseConfig{
			"primary": {
				Driver:    manifest.DriverPostgres,
				URLEnvVar: "APP_DB_URL",
				Domains:   map[string][]string{"events": {"event_outbox"}},
			},
		},
	}
	schemas := map[string]*schema.ReflectedSchema{"primary:public": nestedOutboxSchema()}

	cfg := Config{ProjectRoot: root, Manifest: m, Domain: "nope"}
	err := runNested(cfg, schemas, "github.com/example/app", Options{})
	if err == nil || !strings.Contains(err.Error(), `domain "nope"`) {
		t.Errorf("expected unknown-domain error, got %v", err)
	}
}

// TestParse_DatabaseAnnotationTolerated pins that the retired @database:
// annotation still parses (into FileAnnotations) so existing queries.sql
// files keep working — it just binds nothing.
func TestParse_DatabaseAnnotationTolerated(t *testing.T) {
	f, err := ParseString("-- @database: primary\n\n-- @func: Get\nSELECT * FROM users WHERE user_id = @user_id;\n")
	if err != nil {
		t.Fatalf("parse with annotation: %v", err)
	}
	if f.FileAnnotations["database"] != "primary" {
		t.Errorf("FileAnnotations[database] = %q, want %q (annotation tolerated but unused)",
			f.FileAnnotations["database"], "primary")
	}
	if len(f.Queries) != 1 {
		t.Fatalf("got %d queries, want 1", len(f.Queries))
	}
}
