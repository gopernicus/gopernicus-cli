// Package sqlite is the SQLite adapter for the gopernicus database port.
// It implements database.Driver, database.Migrator, and database.Reflector
// using database/sql with the pure-Go modernc.org/sqlite driver.
//
// # Connection path convention
//
// The database URL configured for a sqlite database (via url_env_var or
// --db-url) is a filesystem path to the database file:
//
//	APP_DB_URL=./data/app.db        # relative — resolved against the project root
//	APP_DB_URL=/var/lib/app/app.db  # absolute — used as-is
//	APP_DB_URL=sqlite://data/app.db # sqlite:// prefix is accepted and stripped
//	APP_DB_URL=file:data/app.db?mode=ro  # file: URI — passed to the driver verbatim
//
// Use ResolvePath to normalize a configured value before calling New.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/gopernicus/gopernicus-cli/internal/database"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// URL prefixes recognized by ResolvePath.
const (
	schemePrefix  = "sqlite://"
	fileURIPrefix = "file:"
)

// Driver is the sqlite implementation of database.Driver,
// database.Migrator, and database.Reflector.
type Driver struct {
	db  *sql.DB
	dsn string
}

// ResolvePath normalizes a configured sqlite database URL into a DSN for New.
// A "sqlite://" prefix is stripped; "file:" URIs are returned verbatim;
// relative paths are resolved against root.
func ResolvePath(raw, root string) string {
	p := strings.TrimPrefix(raw, schemePrefix)
	if strings.HasPrefix(p, fileURIPrefix) {
		return p
	}
	if !filepath.IsAbs(p) {
		return filepath.Join(root, p)
	}
	return p
}

// New opens the sqlite database file at dsn and returns a Driver.
// Plain file paths must already exist — opening a missing file would silently
// create an empty database, which is never what reflection wants. "file:"
// URIs are passed to the driver verbatim without the existence check, since
// their query parameters (e.g. mode=ro) control open behavior.
// The caller must call Close() when done.
func New(ctx context.Context, dsn string) (*Driver, error) {
	if !strings.HasPrefix(dsn, fileURIPrefix) {
		if _, err := os.Stat(dsn); err != nil {
			return nil, fmt.Errorf("sqlite: database file %s does not exist: %w", dsn, err)
		}
	}
	return open(ctx, dsn)
}

// NewCreateIfMissing opens the sqlite database file at dsn, creating it (and
// any missing parent directories) when it does not exist. This is the
// migration-path counterpart to New: 'db migrate' against a missing file
// bootstraps a fresh database, whereas 'db reflect' (which uses New) refuses
// to — reflecting an implicitly created empty database is never useful.
// "file:" URIs are passed to the driver verbatim, as in New.
// The caller must call Close() when done.
func NewCreateIfMissing(ctx context.Context, dsn string) (*Driver, error) {
	if !strings.HasPrefix(dsn, fileURIPrefix) {
		if dir := filepath.Dir(dsn); dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("sqlite: creating directory for %s: %w", dsn, err)
			}
		}
	}
	return open(ctx, dsn)
}

// open opens dsn without any existence checks; the modernc driver creates
// the file if it does not exist.
func open(ctx context.Context, dsn string) (*Driver, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: opening database %s: %w", dsn, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: pinging database %s: %w", dsn, err)
	}
	return &Driver{db: db, dsn: dsn}, nil
}

// Ping verifies the connection is alive.
func (d *Driver) Ping(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

// Close releases the underlying connection pool.
func (d *Driver) Close() {
	d.db.Close()
}

// DBName returns the database file's base name without its extension
// (e.g. "/var/lib/app/app.db" → "app"), mirroring how the Postgres adapter
// reports the connected database name.
func (d *Driver) DBName() string {
	p := strings.TrimPrefix(d.dsn, fileURIPrefix)
	p = strings.TrimPrefix(p, "//")
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	base := filepath.Base(p)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// Reflect reads the live schema from the database.
//
// SQLite has no user-defined schema namespaces equivalent to Postgres
// schemas — reflection always reads the "main" database. The snapshot is
// stamped with the requested schemaName (typically "public") so that file
// naming (_public.json) and the generators' "db:schema" keys stay identical
// across drivers.
func (d *Driver) Reflect(ctx context.Context, schemaName string) (*schema.ReflectedSchema, error) {
	return reflectSchema(ctx, d.db, d.DBName(), schemaName)
}

// RunMigrations applies all pending SQL migration files from dir in order.
// Each migration runs in its own BEGIN IMMEDIATE transaction; applied files
// are recorded in schema_migrations with a sha256 checksum, and modifying an
// applied file is a hard error.
func (d *Driver) RunMigrations(ctx context.Context, dir string) error {
	return runMigrations(ctx, d.db, dir)
}

// MigrationStatus returns the state of every migration file in dir.
func (d *Driver) MigrationStatus(ctx context.Context, dir string) ([]database.MigrationStatus, error) {
	raw, err := migrationStatuses(ctx, d.db, dir)
	if err != nil {
		return nil, err
	}

	out := make([]database.MigrationStatus, len(raw))
	for i, s := range raw {
		out[i] = database.MigrationStatus{
			Version:  s.version,
			Applied:  s.applied,
			Checksum: s.checksum,
			Tampered: s.tampered,
		}
	}
	return out, nil
}
