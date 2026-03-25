// Package manifest handles reading and writing the gopernicus.yml project manifest.
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
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
}

// OutboxEnabled returns true if the event outbox pattern is configured.
func (e *EventsConfig) OutboxEnabled() bool {
	return e != nil && e.Outbox.Enabled()
}

// DatabaseConfig defines a named database connection.
type DatabaseConfig struct {
	// Driver identifies the database adapter (e.g., "postgres/pgx").
	Driver string `yaml:"driver"`

	// URLEnvVar is the environment variable name holding the connection URL
	// (e.g., "DATABASE_URL"). Looked up directly — no namespace prefix.
	URLEnvVar string `yaml:"url_env_var"`

	// Schemas lists the database schemas to reflect (default: ["public"]).
	Schemas []string `yaml:"schemas,omitempty"`

	// Domains maps domain names to table lists for organizing repositories.
	// e.g. auth: [users, principals, credentials]
	Domains map[string][]string `yaml:"domains,omitempty"`
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

// New returns a new manifest with sensible defaults.
func New() *Manifest {
	return NewWithProject("")
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
				Driver:    "postgres/pgx",
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

// DomainForTable returns the domain name for a table within a database config.
// Returns "" if the table is not mapped to any domain.
func (d *DatabaseConfig) DomainForTable(tableName string) string {
	for domain, tables := range d.Domains {
		for _, t := range tables {
			if t == tableName {
				return domain
			}
		}
	}
	return ""
}

// MigrationsDir returns the migrations directory for a named database,
// relative to the project root. e.g. "workshop/migrations/primary".
func MigrationsDir(dbName string) string {
	if dbName == "" {
		dbName = "primary"
	}
	return filepath.Join("workshop", "migrations", dbName)
}
