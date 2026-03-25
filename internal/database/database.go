// Package database defines the port interfaces for gopernicus CLI database operations.
//
// Adapters live in sub-packages named by database engine and Go driver:
//
//	internal/database/postgres/pgx  — PostgreSQL via pgx/v5
//
// Commands depend only on this package, never on a specific adapter.
// Each capability is a separate interface; adapters implement only what their
// database engine supports.
//
//	Driver       — every store must implement (connection lifecycle)
//	Migrator     — SQL databases: apply/track schema migration files
//	Reflector    — SQL databases: read live schema into ReflectedSchema
//	IndexManager — document/NoSQL stores: sync and inspect index definitions
package database

import (
	"context"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// Driver is the base interface every database adapter must implement.
// It covers connection lifecycle only.
type Driver interface {
	// Ping verifies the connection is alive.
	Ping(ctx context.Context) error

	// Close releases the underlying connection pool.
	Close()

	// DBName returns the connected database name.
	DBName() string
}

// Migrator is implemented by SQL databases that support forward-only file-based migrations.
type Migrator interface {
	// RunMigrations applies all pending SQL migration files from dir in order.
	RunMigrations(ctx context.Context, dir string) error

	// MigrationStatus returns the current state of every migration file in dir.
	MigrationStatus(ctx context.Context, dir string) ([]MigrationStatus, error)
}

// Reflector is implemented by databases whose schema can be read at runtime.
type Reflector interface {
	// Reflect reads the live schema and returns a structured representation.
	// schemaName is typically "public" for Postgres.
	Reflect(ctx context.Context, schemaName string) (*schema.ReflectedSchema, error)
}

// IndexManager is implemented by document/NoSQL stores that manage indexes
// through definition files rather than SQL migrations.
type IndexManager interface {
	// SyncIndexes applies index definitions from indexDir to the database.
	SyncIndexes(ctx context.Context, indexDir string) error

	// IndexStatus returns the current state of indexes relative to definitions.
	IndexStatus(ctx context.Context, indexDir string) ([]IndexStatus, error)
}

// MigrationStatus describes a single migration file.
type MigrationStatus struct {
	// Version is the filename (e.g. "20260224120000_create_users.sql").
	Version string

	// Applied is true if the migration has been executed.
	Applied bool

	// Checksum is the first 16 hex characters of the SHA-256 of the file contents.
	Checksum string

	// Tampered is true when the file has been modified after being applied
	// (the on-disk checksum no longer matches the recorded checksum).
	Tampered bool
}

// IndexStatus describes a single index definition.
type IndexStatus struct {
	// Name is the index name as defined in the index file.
	Name string

	// Synced is true if the live index matches the definition.
	Synced bool

	// Definition is the index definition as seen in the database.
	Definition string
}
