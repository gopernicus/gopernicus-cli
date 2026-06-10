package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// writeDispatchProject lays out a minimal project tree with the golden users
// entity and returns (projectRoot, queriesPath, schemas).
func writeDispatchProject(t *testing.T) (string, string, map[string]*schema.ReflectedSchema) {
	t.Helper()
	root := t.TempDir()

	repoDir := filepath.Join(root, "core", "repositories", "auth", "users")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	qfPath := filepath.Join(repoDir, "queries.sql")
	if err := os.WriteFile(qfPath, []byte(specStoreUsersQueries), 0644); err != nil {
		t.Fatalf("write queries.sql: %v", err)
	}

	schemas := map[string]*schema.ReflectedSchema{
		"primary:public": specStoreUsersSchema(),
	}
	return root, qfPath, schemas
}

// dispatchManifest binds auth/users to a single primary database with the
// given driver.
func dispatchManifest(driver string) *manifest.Manifest {
	return &manifest.Manifest{
		Databases: map[string]*manifest.DatabaseConfig{
			"primary": {
				Driver:    driver,
				URLEnvVar: "APP_DB_URL",
				Domains:   map[string][]string{"auth": {"users"}},
			},
		},
	}
}

// TestGenerateDispatch_SpecMode verifies that a sqlite database routes store
// generation to the spec store (no pgx store) and wires the composite to the
// spec package.
func TestGenerateDispatch_SpecMode(t *testing.T) {
	root, qfPath, schemas := writeDispatchProject(t)

	cfg := Config{ProjectRoot: root, Manifest: dispatchManifest(manifest.DriverSQLite)}
	if err := runNested(cfg, schemas, "github.com/example/app", Options{}); err != nil {
		t.Fatalf("runNested: %v", err)
	}

	repoDir := filepath.Dir(qfPath)
	for _, want := range []string{
		filepath.Join(repoDir, "usersstore", "generated.go"),
		filepath.Join(repoDir, "usersstore", "store.go"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected spec store file %s: %v", want, err)
		}
	}

	pgxDir := filepath.Join(repoDir, "userspgx")
	if _, err := os.Stat(pgxDir); !os.IsNotExist(err) {
		t.Errorf("pgx store dir %s must not exist in spec mode", pgxDir)
	}
	// Spec mode generates its own integration tests (testsqlite-backed).
	for _, want := range []string{
		filepath.Join(repoDir, "usersstore", "generated_test.go"),
		filepath.Join(repoDir, "usersstore", "store_test.go"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected spec integration test file %s: %v", want, err)
		}
	}

	// The composite must import the spec store package, not the pgx one.
	composite := mustReadFile(t, filepath.Join(root, "core", "repositories", "auth", "generated_composite.go"))
	if !strings.Contains(composite, "usersstore.NewStore(") {
		t.Errorf("composite must wire the spec store\n--- output ---\n%s", composite)
	}
}

// TestGenerateDispatch_PgxMode verifies the default (postgres) path is
// unchanged: pgx store plus integration tests, no spec store package.
func TestGenerateDispatch_PgxMode(t *testing.T) {
	root, qfPath, schemas := writeDispatchProject(t)

	cfg := Config{ProjectRoot: root, Manifest: dispatchManifest(manifest.DriverPostgres)}
	if err := runNested(cfg, schemas, "github.com/example/app", Options{}); err != nil {
		t.Fatalf("runNested: %v", err)
	}

	repoDir := filepath.Dir(qfPath)
	for _, want := range []string{
		filepath.Join(repoDir, "userspgx", "generated.go"),
		filepath.Join(repoDir, "userspgx", "generated_test.go"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected pgx store file %s: %v", want, err)
		}
	}

	specDir := filepath.Join(repoDir, "usersstore")
	if _, err := os.Stat(specDir); !os.IsNotExist(err) {
		t.Errorf("spec store dir %s must not exist in pgx mode", specDir)
	}

	composite := mustReadFile(t, filepath.Join(root, "core", "repositories", "auth", "generated_composite.go"))
	if !strings.Contains(composite, "userspgx.NewStore(") {
		t.Errorf("composite must wire the pgx store\n--- output ---\n%s", composite)
	}
}

// TestGenerateDispatch_UnrecognizedDriver verifies manifest validation
// surfaces before any generation happens.
func TestGenerateDispatch_UnrecognizedDriver(t *testing.T) {
	root, qfPath, schemas := writeDispatchProject(t)

	cfg := Config{ProjectRoot: root, Manifest: dispatchManifest("mysql")}
	err := runNested(cfg, schemas, "github.com/example/app", Options{})
	if err == nil {
		t.Fatal("expected unrecognized-driver error, got nil")
	}
	if !strings.Contains(err.Error(), "unrecognized database driver") {
		t.Errorf("error %q missing driver context", err)
	}

	repoDir := filepath.Dir(qfPath)
	if _, err := os.Stat(filepath.Join(repoDir, "generated.go")); !os.IsNotExist(err) {
		t.Errorf("driver validation must fail before any file is written")
	}
}

// TestRun_RequiresNestedDomains pins the hard error when gopernicus.yml binds
// nothing under databases.<name>.domains (including the retired top-level
// domains shape).
func TestRun_RequiresNestedDomains(t *testing.T) {
	root, _, _ := writeDispatchProject(t)

	m := &manifest.Manifest{
		Databases: map[string]*manifest.DatabaseConfig{
			"primary": {Driver: manifest.DriverPostgres, URLEnvVar: "APP_DB_URL"},
		},
	}

	err := Run(Config{ProjectRoot: root, Manifest: m})
	if err == nil {
		t.Fatal("expected missing nested-domains error, got nil")
	}
	if !strings.Contains(err.Error(), "databases.<name>.domains") {
		t.Errorf("error %q must point at databases.<name>.domains", err)
	}
}
