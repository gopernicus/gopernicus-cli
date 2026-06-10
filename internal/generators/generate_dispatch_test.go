package generators

import (
	"os"
	"path/filepath"
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

// TestGenerateDispatch_SpecMode verifies that a sqlite database routes store
// generation to the spec store (no pgx store, no integration tests) and
// repoints the composite StorePkg at the spec package.
func TestGenerateDispatch_SpecMode(t *testing.T) {
	root, qfPath, schemas := writeDispatchProject(t)

	m := &manifest.Manifest{
		Databases: map[string]*manifest.DatabaseConfig{
			"primary": {Driver: manifest.DriverSQLite, URLEnvVar: "APP_DB_URL"},
		},
	}

	resolved, storeMode, err := generateFromQueryFile(qfPath, schemas, m, "github.com/example/app", root, false, Options{})
	if err != nil {
		t.Fatalf("generateFromQueryFile: %v", err)
	}
	if resolved == nil {
		t.Fatal("entity was skipped")
	}
	if storeMode != manifest.StoreModeSpec {
		t.Errorf("store mode = %q, want %q", storeMode, manifest.StoreModeSpec)
	}
	if resolved.StorePkg != "usersstore" {
		t.Errorf("StorePkg = %q, want %q (composite must import the spec store)", resolved.StorePkg, "usersstore")
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
}

// TestGenerateDispatch_PgxMode verifies the default (postgres) path is
// unchanged: pgx store plus integration tests, no spec store package.
func TestGenerateDispatch_PgxMode(t *testing.T) {
	root, qfPath, schemas := writeDispatchProject(t)

	m := &manifest.Manifest{
		Databases: map[string]*manifest.DatabaseConfig{
			"primary": {Driver: manifest.DriverPostgres, URLEnvVar: "APP_DB_URL"},
		},
	}

	resolved, storeMode, err := generateFromQueryFile(qfPath, schemas, m, "github.com/example/app", root, false, Options{})
	if err != nil {
		t.Fatalf("generateFromQueryFile: %v", err)
	}
	if resolved == nil {
		t.Fatal("entity was skipped")
	}
	if storeMode != manifest.StoreModePgx {
		t.Errorf("store mode = %q, want %q", storeMode, manifest.StoreModePgx)
	}
	if resolved.StorePkg != "userspgx" {
		t.Errorf("StorePkg = %q, want %q", resolved.StorePkg, "userspgx")
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
}

// TestGenerateDispatch_UnrecognizedDriver verifies manifest validation
// surfaces before any generation happens.
func TestGenerateDispatch_UnrecognizedDriver(t *testing.T) {
	root, qfPath, schemas := writeDispatchProject(t)

	m := &manifest.Manifest{
		Databases: map[string]*manifest.DatabaseConfig{
			"primary": {Driver: "mysql", URLEnvVar: "APP_DB_URL"},
		},
	}

	if _, _, err := generateFromQueryFile(qfPath, schemas, m, "github.com/example/app", root, false, Options{}); err == nil {
		t.Fatal("expected unrecognized-driver error, got nil")
	}
}
