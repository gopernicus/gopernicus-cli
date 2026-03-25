package pgx

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	pgxdriver "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runMigrations(ctx context.Context, pool *pgxpool.Pool, migrationsDir string) error {
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	files, err := sqlFiles(migrationsDir)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		fmt.Println("  No migration files found.")
		return nil
	}

	for _, filename := range files {
		if err := applyOne(ctx, pool, migrationsDir, filename); err != nil {
			return err
		}
	}

	return nil
}

type migrationStatus struct {
	version  string
	applied  bool
	checksum string
	tampered bool
}

func migrationStatuses(ctx context.Context, pool *pgxpool.Pool, migrationsDir string) ([]migrationStatus, error) {
	files, err := sqlFiles(migrationsDir)
	if err != nil {
		return nil, err
	}

	applied := map[string]string{} // version → checksum
	if pool != nil {
		if err := ensureMigrationsTable(ctx, pool); err == nil {
			rows, err := pool.Query(ctx, "SELECT version, checksum FROM schema_migrations ORDER BY version")
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
	}

	var statuses []migrationStatus
	for _, filename := range files {
		content, err := os.ReadFile(filepath.Join(migrationsDir, filename))
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

func applyOne(ctx context.Context, pool *pgxpool.Pool, dir, filename string) error {
	content, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		return fmt.Errorf("reading %s: %w", filename, err)
	}
	checksum := sha256hex(content)

	var existingChecksum string
	err = pool.QueryRow(ctx,
		"SELECT checksum FROM schema_migrations WHERE version = $1", filename,
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
		fmt.Printf("  skip  %s (already applied)\n", filename)
		return nil
	}

	if err != pgxdriver.ErrNoRows {
		return fmt.Errorf("checking migration status for %s: %w", filename, err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction for %s: %w", filename, err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(content)); err != nil {
		return fmt.Errorf("executing %s: %w\n\nSQL:\n%s", filename, err, content)
	}

	if _, err := tx.Exec(ctx,
		"INSERT INTO schema_migrations (version, checksum, raw_sql) VALUES ($1, $2, $3)",
		filename, checksum, string(content),
	); err != nil {
		return fmt.Errorf("recording %s: %w", filename, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing %s: %w", filename, err)
	}

	fmt.Printf("  apply %s\n", filename)
	return nil
}

func ensureMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    VARCHAR(255) PRIMARY KEY,
			checksum   VARCHAR(64)  NOT NULL,
			raw_sql    TEXT,
			applied_at TIMESTAMP    NOT NULL DEFAULT NOW()
		)
	`)
	return err
}

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
