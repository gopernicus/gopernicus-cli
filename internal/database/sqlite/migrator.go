package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// migrationsTableDDL is the schema_migrations tracking table. It mirrors the
// Postgres migrator's table shape (version, checksum, raw_sql, applied_at)
// using SQLite-native types; applied_at defaults to an ISO-8601 UTC timestamp.
const migrationsTableDDL = `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		checksum   TEXT NOT NULL,
		raw_sql    TEXT,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)
`

// runMigrations applies pending SQL migration files from dir in alphabetical
// order. This is a forward-only system — no rollbacks. Already-applied
// migrations are tracked in schema_migrations; sha256 checksums make
// modification of a previously applied file a hard error.
//
// All work happens on a single pinned *sql.Conn. Each migration is applied
// inside its own BEGIN IMMEDIATE transaction: the IMMEDIATE lock serializes
// concurrent runners against the same file (a second process blocks at BEGIN,
// then sees the migration already applied and skips it), and because SQLite
// DDL is transactional, a failing migration rolls back atomically — including
// its schema_migrations row.
func runMigrations(ctx context.Context, db *sql.DB, dir string) error {
	files, err := sqlFiles(dir)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		fmt.Println("  No migration files found.")
		return nil
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquiring migration connection: %w", err)
	}
	defer conn.Close()

	// Wait for concurrent writers instead of failing immediately with
	// SQLITE_BUSY (e.g. another process booting against the same file).
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("setting busy_timeout: %w", err)
	}

	if err := ensureMigrationsTable(ctx, conn); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	for _, filename := range files {
		if err := applyOne(ctx, conn, dir, filename); err != nil {
			return err
		}
	}

	return nil
}

// applyOne applies a single migration file inside a BEGIN IMMEDIATE
// transaction on conn. Already-applied files are verified against their
// recorded checksum and skipped; a mismatch is a hard error.
func applyOne(ctx context.Context, conn *sql.Conn, dir, filename string) error {
	content, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		return fmt.Errorf("reading %s: %w", filename, err)
	}
	checksum := sha256hex(content)

	// BEGIN IMMEDIATE acquires the write lock up front, so the
	// applied-check and the apply are atomic with respect to concurrent
	// runners.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin transaction for %s: %w", filename, err)
	}

	committed := false
	defer func() {
		if !committed {
			// Best-effort rollback with a fresh context: ctx may already
			// be cancelled on the error path.
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var existingChecksum string
	err = conn.QueryRowContext(ctx,
		"SELECT checksum FROM schema_migrations WHERE version = ?", filename,
	).Scan(&existingChecksum)

	if err == nil {
		if existingChecksum != checksum {
			return fmt.Errorf(
				"CHECKSUM MISMATCH: %s was modified after being applied\n"+
					"  applied:  %s\n"+
					"  on disk:  %s\n\n"+
					"  Do not modify applied migrations. Create a new migration instead.",
				filename, existingChecksum[:16], checksum[:16],
			)
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return fmt.Errorf("committing %s: %w", filename, err)
		}
		committed = true
		fmt.Printf("  skip  %s (already applied)\n", filename)
		return nil
	}

	if err != sql.ErrNoRows {
		return fmt.Errorf("checking migration status for %s: %w", filename, err)
	}

	// ExecContext handles multi-statement SQL: with no bind args the
	// modernc.org/sqlite driver executes each semicolon-separated
	// statement in turn.
	if _, err := conn.ExecContext(ctx, string(content)); err != nil {
		return fmt.Errorf("executing %s: %w\n\nSQL:\n%s", filename, err, content)
	}

	if _, err := conn.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, checksum, raw_sql) VALUES (?, ?, ?)",
		filename, checksum, string(content),
	); err != nil {
		return fmt.Errorf("recording %s: %w", filename, err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("committing %s: %w", filename, err)
	}
	committed = true

	fmt.Printf("  apply %s\n", filename)
	return nil
}

// migrationStatuses reports the state of every migration file in dir. The
// applied/checksum lookup against the database is best-effort: when it fails
// (e.g. brand-new file with no schema_migrations table), every file is
// reported as pending.
func migrationStatuses(ctx context.Context, db *sql.DB, dir string) ([]migrationStatus, error) {
	files, err := sqlFiles(dir)
	if err != nil {
		return nil, err
	}

	applied := map[string]string{} // version → checksum
	if db != nil {
		rows, err := db.QueryContext(ctx,
			"SELECT version, checksum FROM schema_migrations ORDER BY version")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var version, checksum string
				if err := rows.Scan(&version, &checksum); err == nil {
					applied[version] = checksum
				}
			}
		}
	}

	var statuses []migrationStatus
	for _, filename := range files {
		content, err := os.ReadFile(filepath.Join(dir, filename))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", filename, err)
		}
		checksum := sha256hex(content)

		s := migrationStatus{
			version:  filename,
			checksum: checksum[:16],
		}

		if appliedChecksum, ok := applied[filename]; ok {
			s.applied = true
			if appliedChecksum != checksum {
				s.tampered = true
			}
		}

		statuses = append(statuses, s)
	}

	return statuses, nil
}

type migrationStatus struct {
	version  string
	applied  bool
	checksum string
	tampered bool
}

func ensureMigrationsTable(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, migrationsTableDDL)
	return err
}

// sqlFiles returns the sorted .sql file names in dir, skipping files with a
// leading underscore (reflection artifacts such as _public.sql).
func sqlFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("migrations directory not found: %s\n\nRun 'gopernicus db create <name>' to create your first migration.", dir)
		}
		return nil, fmt.Errorf("reading migrations directory: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && !strings.HasPrefix(e.Name(), "_") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}
