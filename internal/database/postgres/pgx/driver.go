// Package pgx is the PostgreSQL adapter for the gopernicus database port.
// It implements database.Driver, database.Migrator, and database.Reflector using pgx/v5.
package pgx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gopernicus/gopernicus-cli/internal/database"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// Driver is the pgx implementation of database.Driver, database.Migrator,
// and database.Reflector.
type Driver struct {
	pool *pgxpool.Pool
}

// New connects to the given Postgres database URL and returns a Driver.
// The caller must call Close() when done.
func New(ctx context.Context, dbURL string) (*Driver, error) {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("postgres/pgx: connecting to database: %w", err)
	}
	return &Driver{pool: pool}, nil
}

// Ping verifies the connection is alive.
func (d *Driver) Ping(ctx context.Context) error {
	return d.pool.Ping(ctx)
}

// Close releases the connection pool.
func (d *Driver) Close() {
	d.pool.Close()
}

// DBName returns the connected database name.
func (d *Driver) DBName() string {
	return d.pool.Config().ConnConfig.Database
}

// Reflect reads the live schema from the database.
func (d *Driver) Reflect(ctx context.Context, schemaName string) (*schema.ReflectedSchema, error) {
	return reflectSchema(ctx, d.pool, d.DBName(), schemaName)
}

// RunMigrations applies all pending SQL migration files from dir.
func (d *Driver) RunMigrations(ctx context.Context, dir string) error {
	return runMigrations(ctx, d.pool, dir)
}

// MigrationStatus returns the state of every migration file in dir.
func (d *Driver) MigrationStatus(ctx context.Context, dir string) ([]database.MigrationStatus, error) {
	raw, err := migrationStatuses(ctx, d.pool, dir)
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
