package manifest

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDriverOrDefault(t *testing.T) {
	tests := []struct {
		name    string
		conf    *DatabaseConfig
		want    string
		wantErr bool
	}{
		{"nil config defaults to postgres", nil, DriverPostgres, false},
		{"empty driver defaults to postgres", &DatabaseConfig{}, DriverPostgres, false},
		{"postgres", &DatabaseConfig{Driver: "postgres"}, DriverPostgres, false},
		{"legacy postgres/pgx normalizes", &DatabaseConfig{Driver: "postgres/pgx"}, DriverPostgres, false},
		{"sqlite", &DatabaseConfig{Driver: "sqlite"}, DriverSQLite, false},
		{"unrecognized driver errors", &DatabaseConfig{Driver: "mysql"}, "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.conf.DriverOrDefault()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DriverOrDefault() = %q, want error", got)
				}
				if !strings.Contains(err.Error(), "unrecognized database driver") {
					t.Errorf("error %q missing driver context", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("DriverOrDefault() error: %v", err)
			}
			if got != tc.want {
				t.Errorf("DriverOrDefault() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStoreMode(t *testing.T) {
	tests := []struct {
		name    string
		conf    *DatabaseConfig
		want    StoreMode
		wantErr string // substring of the expected error, "" for success
	}{
		{"nil config defaults to pgx", nil, StoreModePgx, ""},
		{"postgres defaults to pgx", &DatabaseConfig{Driver: "postgres"}, StoreModePgx, ""},
		{"legacy postgres/pgx defaults to pgx", &DatabaseConfig{Driver: "postgres/pgx"}, StoreModePgx, ""},
		{"postgres explicit pgx", &DatabaseConfig{Driver: "postgres", Store: "pgx"}, StoreModePgx, ""},
		{"postgres opts into spec", &DatabaseConfig{Driver: "postgres", Store: "spec"}, StoreModeSpec, ""},
		{"sqlite always spec", &DatabaseConfig{Driver: "sqlite"}, StoreModeSpec, ""},
		{"sqlite explicit spec", &DatabaseConfig{Driver: "sqlite", Store: "spec"}, StoreModeSpec, ""},
		{"sqlite rejects pgx", &DatabaseConfig{Driver: "sqlite", Store: "pgx"}, "", "always uses store mode"},
		{"unrecognized store mode errors", &DatabaseConfig{Driver: "postgres", Store: "gorm"}, "", "unrecognized store mode"},
		{"unrecognized driver propagates", &DatabaseConfig{Driver: "mysql"}, "", "unrecognized database driver"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.conf.StoreMode()
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("StoreMode() = %q, want error containing %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error %q does not contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("StoreMode() error: %v", err)
			}
			if got != tc.want {
				t.Errorf("StoreMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDomainShapes_Nested covers the nested shape: domains declared under
// each database become the sole binding source.
func TestDomainShapes_Nested(t *testing.T) {
	const src = `
version: "1"
databases:
  primary:
    driver: postgres
    domains:
      auth: [users, sessions]
      events: [event_outbox]
  otherdb:
    driver: sqlite
    domains:
      events: [event_outbox]
`
	var m Manifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !m.NestedDomainsDeclared() {
		t.Error("NestedDomainsDeclared() = false, want true")
	}
	if len(m.DomainShapeWarnings()) != 0 {
		t.Errorf("DomainShapeWarnings() = %v, want none", m.DomainShapeWarnings())
	}

	want := map[string][]string{
		"auth":   {"users", "sessions"},
		"events": {"event_outbox"},
	}
	if got := m.EffectiveDomains("primary"); !reflect.DeepEqual(got, want) {
		t.Errorf("EffectiveDomains(primary) = %v, want %v", got, want)
	}
	wantOther := map[string][]string{"events": {"event_outbox"}}
	if got := m.EffectiveDomains("otherdb"); !reflect.DeepEqual(got, wantOther) {
		t.Errorf("EffectiveDomains(otherdb) = %v, want %v", got, wantOther)
	}
}

// TestDomainShapes_Legacy covers the legacy shape: a top-level domains map
// shared by every database (binding comes from @database: annotations).
func TestDomainShapes_Legacy(t *testing.T) {
	const src = `
version: "1"
domains:
  auth: [users]
databases:
  primary:
    driver: postgres
  otherdb:
    driver: sqlite
`
	var m Manifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m.NestedDomainsDeclared() {
		t.Error("NestedDomainsDeclared() = true, want false")
	}
	if len(m.DomainShapeWarnings()) != 0 {
		t.Errorf("DomainShapeWarnings() = %v, want none", m.DomainShapeWarnings())
	}

	want := map[string][]string{"auth": {"users"}}
	for _, db := range []string{"primary", "otherdb"} {
		if got := m.EffectiveDomains(db); !reflect.DeepEqual(got, want) {
			t.Errorf("EffectiveDomains(%s) = %v, want %v (legacy top-level shared by all databases)", db, got, want)
		}
	}
}

// TestDomainShapes_BothWarn covers both shapes at once: the nested shape wins
// and a warning reports the ignored top-level key.
func TestDomainShapes_BothWarn(t *testing.T) {
	const src = `
version: "1"
domains:
  legacy: [old_table]
databases:
  primary:
    driver: postgres
    domains:
      auth: [users]
`
	var m Manifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	warnings := m.DomainShapeWarnings()
	if len(warnings) != 1 {
		t.Fatalf("DomainShapeWarnings() = %v, want exactly one warning", warnings)
	}
	if !strings.Contains(warnings[0], "ignored") {
		t.Errorf("warning %q does not say the top-level key is ignored", warnings[0])
	}

	want := map[string][]string{"auth": {"users"}}
	if got := m.EffectiveDomains("primary"); !reflect.DeepEqual(got, want) {
		t.Errorf("EffectiveDomains(primary) = %v, want nested %v", got, want)
	}
}

// TestDatabaseNamesPrimaryFirst pins the canonical iteration order: sorted,
// with a database literally named "primary" always first.
func TestDatabaseNamesPrimaryFirst(t *testing.T) {
	tests := []struct {
		name string
		dbs  []string
		want []string
	}{
		{"primary sorts first", []string{"zeta", "alpha", "primary"}, []string{"primary", "alpha", "zeta"}},
		{"no primary stays sorted", []string{"zeta", "alpha"}, []string{"alpha", "zeta"}},
		{"primary alone", []string{"primary"}, []string{"primary"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Databases: map[string]*DatabaseConfig{}}
			for _, db := range tc.dbs {
				m.Databases[db] = &DatabaseConfig{}
			}
			if got := m.DatabaseNamesPrimaryFirst(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DatabaseNamesPrimaryFirst() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestNewWithProject_DriverDefault pins the scaffolded driver to the
// documented default spelling.
func TestNewWithProject_DriverDefault(t *testing.T) {
	m := NewWithProject("myapp")
	conf := m.DatabaseOrDefault("")
	if conf == nil {
		t.Fatal("no primary database in scaffolded manifest")
	}
	if conf.Driver != DriverPostgres {
		t.Errorf("scaffolded driver = %q, want %q", conf.Driver, DriverPostgres)
	}
	mode, err := conf.StoreMode()
	if err != nil {
		t.Fatalf("StoreMode() error: %v", err)
	}
	if mode != StoreModePgx {
		t.Errorf("scaffolded store mode = %q, want %q", mode, StoreModePgx)
	}
}
