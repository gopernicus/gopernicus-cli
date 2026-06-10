// Package manifest handles reading and writing the gopernicus.yml project manifest.
//
// # Domain shape
//
// Domains bind entities (tables) to databases, declared under each database
// (databases.<name>.domains). The manifest is the sole binding source:
//
//	databases:
//	  primary:
//	    driver: postgres
//	    domains:
//	      auth: [users, sessions]
//	      events: [event_outbox]
//	  otherdb:
//	    driver: sqlite
//	    domains:
//	      events: [event_outbox]   # same entity, second database — allowed
//
// (Earlier scaffolds wrote a single top-level domains map bound via
// `@database:` annotations; that shape is no longer supported — generation
// errors until the tables are declared under their databases.)
//
// Databases iterate in the canonical order returned by
// DatabaseNamesPrimaryFirst: sorted names, except a database literally named
// "primary" always sorts first. When an entity is declared under more than
// one database, the first declaring database in that order is its canonical
// database — its reflected schema snapshot drives all generation for the
// entity.
//
// # Database driver and store mode
//
// Each database declares a driver, which selects both the connection adapter
// and the default generation store mode for every repository bound to it:
//
//	databases:
//	  primary:
//	    driver: postgres   # default — generates the pgx store
//	    store: spec        # optional opt-in to the dialect-neutral spec store
//	  embedded:
//	    driver: sqlite     # always uses the spec store mode
//
// Recognized drivers are "postgres" (the default; the legacy "postgres/pgx"
// spelling from earlier scaffolds is accepted) and "sqlite". The optional
// `store` key is only meaningful for postgres: "pgx" (the default) generates
// the pgx-coupled store, "spec" generates the dialect-neutral crud spec
// store. sqlite has no pgx store, so it always resolves to spec mode and
// rejects `store: pgx`.
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Recognized database drivers (DatabaseConfig.Driver).
const (
	// DriverPostgres is the default driver.
	DriverPostgres = "postgres"
	// DriverSQLite always resolves to the spec store mode.
	DriverSQLite = "sqlite"

	// driverPostgresPgx is the legacy spelling written by earlier scaffolds;
	// it normalizes to DriverPostgres.
	driverPostgresPgx = "postgres/pgx"
)

// StoreMode selects which data-layer generator runs for a database's
// repositories.
type StoreMode string

const (
	// StoreModePgx generates the pgx-coupled store (postgres only).
	StoreModePgx StoreMode = "pgx"
	// StoreModeSpec generates the dialect-neutral crud spec store.
	StoreModeSpec StoreMode = "spec"
)

// Feature represents a feature toggle that can be a bool or a provider string.
// true defaults to "gopernicus", false or "" means disabled, any other string
// names the provider.
type Feature string

const FeatureGopernicus Feature = "gopernicus"

// Enabled returns true if a provider is configured.
func (f Feature) Enabled() bool { return f != "" }

// Provider returns the provider name, or empty string if disabled.
func (f Feature) Provider() string { return string(f) }

// UnmarshalYAML handles both bool and string values.
func (f *Feature) UnmarshalYAML(value *yaml.Node) error {
	switch value.Tag {
	case "!!bool":
		var b bool
		if err := value.Decode(&b); err != nil {
			return err
		}
		if b {
			*f = FeatureGopernicus
		} else {
			*f = ""
		}
	default:
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		*f = Feature(s)
	}
	return nil
}

const Filename = "gopernicus.yml"

// Manifest is the root structure of gopernicus.yml.
type Manifest struct {
	Version           string                     `yaml:"version"`
	GopernicusVersion string                     `yaml:"gopernicus_version,omitempty"`
	EnvFile           string                     `yaml:"env_file,omitempty"`
	Databases         map[string]*DatabaseConfig `yaml:"databases,omitempty"`
	Features          *FeaturesConfig            `yaml:"features,omitempty"`
	Events            *EventsConfig              `yaml:"events,omitempty"`
}

// EventsConfig configures the event infrastructure for a gopernicus project.
// Events (in-memory bus) are always present; this section opts into specific
// persistence and delivery patterns.
type EventsConfig struct {
	// Outbox enables the event outbox pattern: events are persisted to the
	// event_outbox table before delivery, guaranteeing at-least-once delivery
	// across process restarts. Accepts "gopernicus" or a provider string.
	Outbox Feature `yaml:"outbox,omitempty"`

	// JobQueue enables the durable job queue: jobs are persisted to the
	// job_queue table for at-least-once processing with retry and
	// dead-lettering. Accepts "gopernicus" or a provider string.
	JobQueue Feature `yaml:"job_queue,omitempty"`
}

// OutboxEnabled returns true if the event outbox pattern is configured.
func (e *EventsConfig) OutboxEnabled() bool {
	return e != nil && e.Outbox.Enabled()
}

// JobQueueEnabled returns true if the job queue is configured.
func (e *EventsConfig) JobQueueEnabled() bool {
	return e != nil && e.JobQueue.Enabled()
}

// DatabaseConfig defines a named database connection.
type DatabaseConfig struct {
	// Driver identifies the database adapter: "postgres" (default) or
	// "sqlite". The legacy "postgres/pgx" spelling normalizes to "postgres".
	Driver string `yaml:"driver"`

	// Store opts a postgres database into a generation store mode: "pgx"
	// (default) or "spec" (the dialect-neutral crud store). Databases with
	// driver "sqlite" always use spec mode and reject "pgx".
	Store string `yaml:"store,omitempty"`

	// URLEnvVar is the environment variable name holding the connection URL
	// (e.g., "DATABASE_URL"). Looked up directly — no namespace prefix.
	URLEnvVar string `yaml:"url_env_var"`

	// Schemas lists the database schemas to reflect (default: ["public"]).
	Schemas []string `yaml:"schemas,omitempty"`

	// Domains maps domain names to table lists for organizing repositories.
	// e.g. auth: [users, principals, credentials]
	Domains map[string][]string `yaml:"domains,omitempty"`
}

// DriverOrDefault returns the validated, normalized driver name. A nil
// config or empty driver defaults to DriverPostgres; the legacy
// "postgres/pgx" spelling normalizes to DriverPostgres. Unrecognized
// drivers return an error.
func (d *DatabaseConfig) DriverOrDefault() (string, error) {
	if d == nil {
		return DriverPostgres, nil
	}
	switch d.Driver {
	case "", DriverPostgres, driverPostgresPgx:
		return DriverPostgres, nil
	case DriverSQLite:
		return DriverSQLite, nil
	default:
		return "", fmt.Errorf(
			"unrecognized database driver %q (recognized: %q, %q)",
			d.Driver, DriverPostgres, DriverSQLite,
		)
	}
}

// StoreMode resolves the generation store mode from the driver and the
// optional store key: sqlite always uses StoreModeSpec; postgres defaults to
// StoreModePgx unless `store: spec` opts in.
func (d *DatabaseConfig) StoreMode() (StoreMode, error) {
	driver, err := d.DriverOrDefault()
	if err != nil {
		return "", err
	}

	store := ""
	if d != nil {
		store = d.Store
	}

	if driver == DriverSQLite {
		switch store {
		case "", string(StoreModeSpec):
			return StoreModeSpec, nil
		default:
			return "", fmt.Errorf(
				"driver %q always uses store mode %q; remove `store: %s`",
				DriverSQLite, StoreModeSpec, store,
			)
		}
	}

	switch store {
	case "", string(StoreModePgx):
		return StoreModePgx, nil
	case string(StoreModeSpec):
		return StoreModeSpec, nil
	default:
		return "", fmt.Errorf(
			"unrecognized store mode %q (recognized: %q, %q)",
			store, StoreModePgx, StoreModeSpec,
		)
	}
}

// SchemasOrDefault returns the configured schemas, defaulting to ["public"].
func (d *DatabaseConfig) SchemasOrDefault() []string {
	if len(d.Schemas) == 0 {
		return []string{"public"}
	}
	return d.Schemas
}

// FeaturesConfig toggles framework feature sets.
//
// Each field accepts true (defaults to "gopernicus"), false (disabled), or a
// provider string (e.g. "auth0"). An empty/zero value means disabled.
type FeaturesConfig struct {
	Authentication Feature `yaml:"authentication,omitempty"`
	Authorization  Feature `yaml:"authorization,omitempty"`
	Tenancy        Feature `yaml:"tenancy,omitempty"`
}

// AuthenticationEnabled returns true if any authentication provider is configured.
func (f *FeaturesConfig) AuthenticationEnabled() bool {
	return f != nil && f.Authentication.Enabled()
}

// AuthorizationEnabled returns true if any authorization provider is configured.
func (f *FeaturesConfig) AuthorizationEnabled() bool {
	return f != nil && f.Authorization.Enabled()
}

// AuthorizationProvider returns the configured authorization provider, or "" if disabled.
func (f *FeaturesConfig) AuthorizationProvider() Feature {
	if f == nil {
		return ""
	}
	return f.Authorization
}

// TenancyEnabled returns true if any tenancy provider is configured.
func (f *FeaturesConfig) TenancyEnabled() bool {
	return f != nil && f.Tenancy.Enabled()
}

// DatabaseOrDefault returns the named database config, falling back to "primary".
// Returns nil if no databases are configured.
func (m *Manifest) DatabaseOrDefault(name string) *DatabaseConfig {
	if m.Databases == nil {
		return nil
	}
	if name == "" {
		name = "primary"
	}
	return m.Databases[name]
}

// DatabaseNames returns all configured database names in sorted order.
func (m *Manifest) DatabaseNames() []string {
	names := make([]string, 0, len(m.Databases))
	for name := range m.Databases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DatabaseNamesPrimaryFirst returns all configured database names in the
// canonical iteration order: sorted, except a database literally named
// "primary" always sorts first. Generation iterates databases in this order;
// the first database that declares an entity is the entity's canonical
// database.
func (m *Manifest) DatabaseNamesPrimaryFirst() []string {
	names := m.DatabaseNames()
	for i, name := range names {
		if name == "primary" && i != 0 {
			copy(names[1:i+1], names[:i])
			names[0] = "primary"
			break
		}
	}
	return names
}

// NestedDomainsDeclared reports whether any database declares a domains map
// (databases.<name>.domains) — the manifest's sole binding source.
func (m *Manifest) NestedDomainsDeclared() bool {
	for _, db := range m.Databases {
		if db != nil && len(db.Domains) > 0 {
			return true
		}
	}
	return false
}

// Load reads gopernicus.yml from the given project root.
func Load(root string) (*Manifest, error) {
	path := filepath.Join(root, Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("gopernicus.yml not found in %s\n\nRun 'gopernicus init' to create a project.", root)
		}
		return nil, fmt.Errorf("reading %s: %w", Filename, err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", Filename, err)
	}
	return &m, nil
}

// Save writes the manifest to gopernicus.yml in the given project root.
func Save(root string, m *Manifest) error {
	path := filepath.Join(root, Filename)
	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("serializing manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", Filename, err)
	}
	return nil
}

// NewWithProject returns a new manifest with the database URL env var
// namespaced to the project. e.g., "myapp" → "MYAPP_DB_DATABASE_URL".
// This matches the env tag on pgxdb.Options (env:"DB_DATABASE_URL").
// An empty projectName falls back to "DATABASE_URL".
func NewWithProject(projectName string) *Manifest {
	urlEnvVar := "DATABASE_URL"
	if projectName != "" {
		prefix := strings.ToUpper(strings.ReplaceAll(projectName, "-", "_"))
		urlEnvVar = prefix + "_DB_DATABASE_URL"
	}

	return &Manifest{
		Version:           "1",
		GopernicusVersion: "latest",
		EnvFile:           ".env",
		Databases: map[string]*DatabaseConfig{
			"primary": {
				Driver:    DriverPostgres,
				URLEnvVar: urlEnvVar,
			},
		},
		Features: &FeaturesConfig{
			Authentication: FeatureGopernicus,
			Authorization:  FeatureGopernicus,
			Tenancy:        FeatureGopernicus,
		},
	}
}

// MigrationsDir returns the migrations directory for a named database,
// relative to the project root. e.g. "workshop/migrations/primary".
func MigrationsDir(dbName string) string {
	if dbName == "" {
		dbName = "primary"
	}
	return filepath.Join("workshop", "migrations", dbName)
}
