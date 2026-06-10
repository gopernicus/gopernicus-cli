package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	migrationOne = `CREATE TABLE users (
	user_id TEXT PRIMARY KEY,
	email   TEXT NOT NULL UNIQUE
);
CREATE INDEX idx_users_email ON users(email);
`
	migrationTwo = `ALTER TABLE users ADD COLUMN display_name TEXT;
`
)

// migrateFixture writes two migration files into a temp migrations dir and
// returns the dir plus the (not yet existing) database file path.
func migrateFixture(t *testing.T) (migrationsDir, dbPath string) {
	t.Helper()
	root := t.TempDir()

	migrationsDir = filepath.Join(root, "workshop", "migrations", "primary")
	if err := os.MkdirAll(migrationsDir, 0755); err != nil {
		t.Fatalf("creating migrations dir: %v", err)
	}

	writeMigration(t, migrationsDir, "20260101000000_create_users.sql", migrationOne)
	writeMigration(t, migrationsDir, "20260102000000_add_display_name.sql", migrationTwo)

	// _-prefixed reflection artifacts must be ignored.
	writeMigration(t, migrationsDir, "_public.sql", "-- reflection artifact, not a migration\n")

	return migrationsDir, filepath.Join(root, "data", "app.db")
}

func writeMigration(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestRunMigrationsEndToEnd(t *testing.T) {
	ctx := context.Background()
	migrationsDir, dbPath := migrateFixture(t)

	// db migrate creates a missing database file (unlike reflect's New).
	if _, err := New(ctx, dbPath); err == nil {
		t.Fatal("New should refuse a missing database file")
	}
	d, err := NewCreateIfMissing(ctx, dbPath)
	if err != nil {
		t.Fatalf("NewCreateIfMissing: %v", err)
	}
	defer d.Close()

	if err := d.RunMigrations(ctx, migrationsDir); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file was not created: %v", err)
	}

	// Both migrations applied in order: table from 0001 has the column added
	// by 0002, and the second migration would fail if run before the first.
	var n int
	if err := d.db.QueryRowContext(ctx,
		"SELECT count(*) FROM pragma_table_info('users') WHERE name = 'display_name'",
	).Scan(&n); err != nil {
		t.Fatalf("inspecting users table: %v", err)
	}
	if n != 1 {
		t.Fatalf("display_name column missing: migrations not applied in order")
	}

	// schema_migrations records both files (and not the _public.sql artifact).
	var versions []string
	rows, err := d.db.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("querying schema_migrations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		versions = append(versions, v)
	}
	want := []string{"20260101000000_create_users.sql", "20260102000000_add_display_name.sql"}
	if len(versions) != len(want) || versions[0] != want[0] || versions[1] != want[1] {
		t.Fatalf("schema_migrations versions = %v, want %v", versions, want)
	}

	var checksum, appliedAt string
	if err := d.db.QueryRowContext(ctx,
		"SELECT checksum, applied_at FROM schema_migrations WHERE version = ?", want[0],
	).Scan(&checksum, &appliedAt); err != nil {
		t.Fatalf("reading migration record: %v", err)
	}
	if checksum != sha256hex([]byte(migrationOne)) {
		t.Fatalf("recorded checksum %s does not match file contents", checksum)
	}
	if appliedAt == "" {
		t.Fatal("applied_at not recorded")
	}

	// Re-running is a no-op: same records, no errors.
	if err := d.RunMigrations(ctx, migrationsDir); err != nil {
		t.Fatalf("re-running migrations: %v", err)
	}
	var count int
	if err := d.db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("counting migration records: %v", err)
	}
	if count != 2 {
		t.Fatalf("re-migrate changed schema_migrations: %d records, want 2", count)
	}
}

func TestRunMigrationsChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	migrationsDir, dbPath := migrateFixture(t)

	d, err := NewCreateIfMissing(ctx, dbPath)
	if err != nil {
		t.Fatalf("NewCreateIfMissing: %v", err)
	}
	defer d.Close()

	if err := d.RunMigrations(ctx, migrationsDir); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Tamper with an applied migration.
	writeMigration(t, migrationsDir, "20260101000000_create_users.sql",
		migrationOne+"-- sneaky edit\n")

	err = d.RunMigrations(ctx, migrationsDir)
	if err == nil {
		t.Fatal("RunMigrations should fail after tampering with an applied file")
	}
	if !strings.Contains(err.Error(), "CHECKSUM MISMATCH") {
		t.Fatalf("error %q does not mention CHECKSUM MISMATCH", err)
	}

	// Status reports the tampered file rather than erroring.
	statuses, err := d.MigrationStatus(ctx, migrationsDir)
	if err != nil {
		t.Fatalf("MigrationStatus: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("got %d statuses, want 2", len(statuses))
	}
	if !statuses[0].Tampered || !statuses[0].Applied {
		t.Fatalf("first migration should be applied+tampered, got %+v", statuses[0])
	}
	if statuses[1].Tampered {
		t.Fatalf("second migration should not be tampered, got %+v", statuses[1])
	}
}

func TestMigrationStatusPendingAndApplied(t *testing.T) {
	ctx := context.Background()
	migrationsDir, dbPath := migrateFixture(t)

	d, err := NewCreateIfMissing(ctx, dbPath)
	if err != nil {
		t.Fatalf("NewCreateIfMissing: %v", err)
	}
	defer d.Close()

	// Before migrating: everything pending (no schema_migrations table yet —
	// the lookup is best-effort).
	statuses, err := d.MigrationStatus(ctx, migrationsDir)
	if err != nil {
		t.Fatalf("MigrationStatus (fresh db): %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("got %d statuses, want 2 (the _public.sql artifact must be skipped)", len(statuses))
	}
	for _, s := range statuses {
		if s.Applied || s.Tampered {
			t.Fatalf("fresh database: %s should be pending, got %+v", s.Version, s)
		}
		if len(s.Checksum) != 16 {
			t.Fatalf("checksum should be 16 hex chars, got %q", s.Checksum)
		}
	}

	if err := d.RunMigrations(ctx, migrationsDir); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Add a new pending migration after the first run.
	writeMigration(t, migrationsDir, "20260103000000_add_orgs.sql",
		"CREATE TABLE orgs (org_id TEXT PRIMARY KEY);\n")

	statuses, err = d.MigrationStatus(ctx, migrationsDir)
	if err != nil {
		t.Fatalf("MigrationStatus: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("got %d statuses, want 3", len(statuses))
	}
	if !statuses[0].Applied || !statuses[1].Applied {
		t.Fatalf("first two migrations should be applied: %+v", statuses[:2])
	}
	if statuses[2].Applied {
		t.Fatalf("new migration should be pending, got %+v", statuses[2])
	}

	// Applying the pending migration brings everything to applied.
	if err := d.RunMigrations(ctx, migrationsDir); err != nil {
		t.Fatalf("RunMigrations (catch-up): %v", err)
	}
	statuses, err = d.MigrationStatus(ctx, migrationsDir)
	if err != nil {
		t.Fatalf("MigrationStatus (after catch-up): %v", err)
	}
	for _, s := range statuses {
		if !s.Applied || s.Tampered {
			t.Fatalf("%s should be applied and untampered, got %+v", s.Version, s)
		}
	}
}

func TestRunMigrationsMissingDir(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "app.db")

	d, err := NewCreateIfMissing(ctx, dbPath)
	if err != nil {
		t.Fatalf("NewCreateIfMissing: %v", err)
	}
	defer d.Close()

	err = d.RunMigrations(ctx, filepath.Join(t.TempDir(), "nope"))
	if err == nil || !strings.Contains(err.Error(), "migrations directory not found") {
		t.Fatalf("want missing-directory error, got %v", err)
	}
}

func TestRunMigrationsFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	migrationsDir, dbPath := migrateFixture(t)

	// A migration that fails midway: first statement succeeds, second is
	// invalid. Both must roll back, including the schema_migrations row.
	writeMigration(t, migrationsDir, "20260103000000_broken.sql",
		"CREATE TABLE half_done (id TEXT PRIMARY KEY);\nTHIS IS NOT SQL;\n")

	d, err := NewCreateIfMissing(ctx, dbPath)
	if err != nil {
		t.Fatalf("NewCreateIfMissing: %v", err)
	}
	defer d.Close()

	if err := d.RunMigrations(ctx, migrationsDir); err == nil {
		t.Fatal("RunMigrations should fail on broken SQL")
	}

	var n int
	if err := d.db.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'half_done'",
	).Scan(&n); err != nil {
		t.Fatalf("checking rollback: %v", err)
	}
	if n != 0 {
		t.Fatal("half_done table exists: failed migration was not rolled back")
	}

	if err := d.db.QueryRowContext(ctx,
		"SELECT count(*) FROM schema_migrations WHERE version = '20260103000000_broken.sql'",
	).Scan(&n); err != nil {
		t.Fatalf("checking schema_migrations: %v", err)
	}
	if n != 0 {
		t.Fatal("broken migration was recorded despite failing")
	}

	// Earlier migrations remain applied (per-migration transactions).
	statuses, err := d.MigrationStatus(ctx, migrationsDir)
	if err != nil {
		t.Fatalf("MigrationStatus: %v", err)
	}
	if !statuses[0].Applied || !statuses[1].Applied {
		t.Fatalf("earlier migrations should remain applied: %+v", statuses[:2])
	}
}
