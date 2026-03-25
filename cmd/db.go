package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gopernicus/gopernicus-cli/internal/database"
	pgxdb "github.com/gopernicus/gopernicus-cli/internal/database/postgres/pgx"
	"github.com/gopernicus/gopernicus-cli/internal/env"
	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/project"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

var dbCmd = &Command{
	Name:  "db",
	Short: "Database utilities (migrate, reflect, status)",
	Long:  "Database utilities for managing migrations and schema reflection.",
	Usage: "gopernicus db <subcommand>",
}

func init() {
	dbCmd.SubCommands = []*Command{
		{
			Name:  "migrate",
			Short: "Run pending migrations",
			Usage: "gopernicus db migrate [--db-url <url>]",
			Run:   runDBMigrate,
		},
		{
			Name:  "reflect",
			Short: "Reflect database schema into migrations directory",
			Long: `Reflect the live database schema and write _schema.json and _schema.sql.

Connects to the database using the env var specified in gopernicus.yml
(databases.{name}.url_env_var), queries the schema metadata, and writes:

  workshop/migrations/{db}/_schema.json  — machine-readable schema (consumed by 'gopernicus generate')
  workshop/migrations/{db}/_schema.sql   — human-readable SQL summary

The .env file at the project root is loaded automatically.
Override the env file path in gopernicus.yml:

  env_file: .env.local`,
			Usage: "gopernicus db reflect [--db-url <url>]",
			Run:   runDBReflect,
		},
		{
			Name:  "status",
			Short: "Show migration status",
			Usage: "gopernicus db status [--db-url <url>]",
			Run:   runDBStatus,
		},
		{
			Name:  "create",
			Short: "Create a new migration file",
			Usage: "gopernicus db create <name>",
			Run:   runDBCreate,
		},
	}
	dbCmd.Run = runDB
	RegisterCommand(dbCmd)
}

func runDB(_ context.Context, args []string) error {
	if len(args) == 0 {
		printCommandHelp(dbCmd)
		return nil
	}
	name := args[0]
	for _, sub := range dbCmd.SubCommands {
		if sub.Name == name {
			rest := args[1:]
			for _, a := range rest {
				if a == "-h" || a == "--help" {
					printCommandHelp(sub)
					return nil
				}
			}
			return sub.Run(context.Background(), rest)
		}
	}
	return fmt.Errorf("unknown db subcommand %q\n\nRun 'gopernicus db --help' for usage.", name)
}

func runDBMigrate(ctx context.Context, args []string) error {
	driver, root, _, err := connectDriver(ctx, args)
	if err != nil {
		return err
	}
	defer driver.Close()

	dbName := dbNameFromArgs(args)
	migrationsDir := filepath.Join(root, manifest.MigrationsDir(dbName))
	fmt.Printf("Running migrations from %s...\n", migrationsDir)

	mg, ok := database.Driver(driver).(database.Migrator)
	if !ok {
		return fmt.Errorf("%s does not support migrations", driver.DBName())
	}
	if err := mg.RunMigrations(ctx, migrationsDir); err != nil {
		return err
	}
	fmt.Println("\n  ✓ migrations complete")
	return nil
}

func runDBReflect(ctx context.Context, args []string) error {
	driver, root, m, err := connectDriver(ctx, args)
	if err != nil {
		return err
	}
	defer driver.Close()

	dbName := dbNameFromArgs(args)
	schemas := []string{"public"}
	if dbConf := m.DatabaseOrDefault(dbName); dbConf != nil {
		schemas = dbConf.SchemasOrDefault()
	}

	ref, ok := database.Driver(driver).(database.Reflector)
	if !ok {
		return fmt.Errorf("%s does not support schema reflection", driver.DBName())
	}

	outDir := filepath.Join(root, manifest.MigrationsDir(dbName))
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", outDir, err)
	}

	for _, schemaName := range schemas {
		fmt.Printf("Reflecting schema '%s' from %s...\n", schemaName, driver.DBName())

		s, err := ref.Reflect(ctx, schemaName)
		if err != nil {
			return fmt.Errorf("reflecting schema %s: %w", schemaName, err)
		}

		fmt.Printf("  %d tables, %d enum types\n", len(s.Tables), len(s.EnumTypes))

		jsonPath := filepath.Join(outDir, "_"+schemaName+".json")
		if err := schema.WriteJSON(s, jsonPath); err != nil {
			return fmt.Errorf("writing %s: %w", jsonPath, err)
		}
		fmt.Printf("  ✓ wrote %s\n", jsonPath)

		sqlPath := filepath.Join(outDir, "_"+schemaName+".sql")
		if err := schema.WriteSQL(s, sqlPath); err != nil {
			return fmt.Errorf("writing %s: %w", sqlPath, err)
		}
		fmt.Printf("  ✓ wrote %s\n", sqlPath)
	}

	return nil
}

func runDBStatus(ctx context.Context, args []string) error {
	root, err := project.MustFindRoot()
	if err != nil {
		return err
	}

	m, err := manifest.Load(root)
	if err != nil {
		return err
	}

	dbName := dbNameFromArgs(args)
	migrationsDir := filepath.Join(root, manifest.MigrationsDir(dbName))

	// DB connection is best-effort for status — show file list even if unavailable
	var statuses []database.MigrationStatus
	if dbURL, urlErr := resolveDBURL(args, m, root); urlErr == nil && dbURL != "" {
		if d, connErr := pgxdb.New(ctx, dbURL); connErr == nil && d.Ping(ctx) == nil {
			defer d.Close()
			if mg, ok := database.Driver(d).(database.Migrator); ok {
				statuses, err = mg.MigrationStatus(ctx, migrationsDir)
				if err != nil {
					return err
				}
			}
		}
	}

	if statuses == nil {
		statuses, err = fileOnlyStatus(migrationsDir)
		if err != nil {
			return err
		}
	}

	if len(statuses) == 0 {
		fmt.Println("No migration files found.")
		return nil
	}

	fmt.Println()
	for _, s := range statuses {
		var symbol, detail string
		switch {
		case s.Tampered:
			symbol = "!"
			detail = fmt.Sprintf("TAMPERED (checksum mismatch: %s)", s.Checksum)
		case s.Applied:
			symbol = "✓"
			detail = s.Checksum
		default:
			symbol = "·"
			detail = "pending"
		}
		fmt.Printf("  %s  %-48s  %s\n", symbol, s.Version, detail)
	}
	fmt.Println()

	return nil
}

func runDBCreate(_ context.Context, args []string) error {
	// Extract --db flag before checking positional args.
	dbName := dbNameFromArgs(args)

	// Filter out flags to find the migration name.
	var name string
	for i := 0; i < len(args); i++ {
		if args[i] == "--db" {
			i++ // skip value
			continue
		}
		if strings.HasPrefix(args[i], "--db=") {
			continue
		}
		if !strings.HasPrefix(args[i], "-") && name == "" {
			name = args[i]
		}
	}

	if name == "" {
		return fmt.Errorf("migration name required\n\nUsage: gopernicus db create <name> [--db <database>]")
	}

	root, err := project.MustFindRoot()
	if err != nil {
		return err
	}

	name = sanitiseMigrationName(name)
	if name == "" {
		return fmt.Errorf("invalid migration name %q", args[0])
	}

	migrationsDir := filepath.Join(root, manifest.MigrationsDir(dbName))
	if err := os.MkdirAll(migrationsDir, 0755); err != nil {
		return fmt.Errorf("creating migrations directory: %w", err)
	}

	timestamp := time.Now().Format("20060102150405")
	filename := fmt.Sprintf("%s_%s.sql", timestamp, name)
	path := filepath.Join(migrationsDir, filename)

	content := fmt.Sprintf("-- Migration: %s\n-- Created: %s\n\n",
		filename, time.Now().Format("2006-01-02 15:04:05"))
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing migration file: %w", err)
	}

	fmt.Printf("\n  ✓ created %s\n\n", path)
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// connectDriver loads the manifest, resolves the DB URL, connects, and pings.
// The dbName selects which database from the manifest (defaults to "primary").
// Returns the driver, project root, and manifest. The caller must call driver.Close().
func connectDriver(ctx context.Context, args []string) (*pgxdb.Driver, string, *manifest.Manifest, error) {
	root, err := project.MustFindRoot()
	if err != nil {
		return nil, "", nil, err
	}

	m, err := manifest.Load(root)
	if err != nil {
		return nil, "", nil, err
	}

	dbURL, err := resolveDBURL(args, m, root)
	if err != nil {
		return nil, "", nil, err
	}

	driver, err := pgxdb.New(ctx, dbURL)
	if err != nil {
		return nil, "", nil, err
	}

	if err := driver.Ping(ctx); err != nil {
		driver.Close()
		return nil, "", nil, fmt.Errorf("pinging database: %w", err)
	}

	return driver, root, m, nil
}

// resolveDBURL determines the database URL from (in priority order):
// 1. --db-url flag (explicit override)
// 2. Manifest database config → env var lookup
// 3. Bare DATABASE_URL env var (legacy fallback)
func resolveDBURL(args []string, m *manifest.Manifest, root string) (string, error) {
	if u := flagValue(args, "--db-url"); u != "" {
		return u, nil
	}

	envCfg := env.New(m.EnvFile, root)
	dbName := flagValue(args, "--db")

	dbConf := m.DatabaseOrDefault(dbName)
	if dbConf != nil && dbConf.URLEnvVar != "" {
		if u := envCfg.Get(dbConf.URLEnvVar); u != "" {
			return u, nil
		}
		return "", fmt.Errorf(
			"database %q: environment variable %s is not set",
			dbName, dbConf.URLEnvVar,
		)
	}

	// Legacy fallback: no databases in manifest, try DATABASE_URL directly.
	return envCfg.Require("DATABASE_URL")
}

// dbNameFromArgs extracts the --db flag value (defaults to "primary").
func dbNameFromArgs(args []string) string {
	if name := flagValue(args, "--db"); name != "" {
		return name
	}
	return "primary"
}

// fileOnlyStatus returns migration file names without DB connection data.
func fileOnlyStatus(dir string) ([]database.MigrationStatus, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []database.MigrationStatus
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && !strings.HasPrefix(e.Name(), "_") {
			out = append(out, database.MigrationStatus{Version: e.Name()})
		}
	}
	return out, nil
}

// flagValue extracts --flag <value> or --flag=<value> from args.
func flagValue(args []string, flag string) string {
	prefix := flag + "="
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, prefix) {
			return a[len(prefix):]
		}
	}
	return ""
}

func sanitiseMigrationName(s string) string {
	var b []byte
	for _, c := range strings.ToLower(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			b = append(b, byte(c))
		case c == ' ' || c == '-':
			b = append(b, '_')
		}
	}
	return string(b)
}
