package manifest

import (
	"strings"
	"testing"
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
