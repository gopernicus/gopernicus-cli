package generators

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/project"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// Config controls what gets generated.
type Config struct {
	ProjectRoot    string
	Manifest       *manifest.Manifest
	Domain         string // if set, only generate repos under this domain subdir
	DryRun         bool
	Verbose        bool
	ForceBootstrap bool
}

// Run executes code generation. The manifest's nested domain shape
// (databases.<name>.domains) is the sole binding source; generation iterates
// database×domain×entity. A manifest without nested domains is an error.
func Run(cfg Config) error {
	if !cfg.Manifest.NestedDomainsDeclared() {
		return fmt.Errorf(
			"gopernicus.yml declares no domains under any database\n\n" +
				"Declare your entities under databases.<name>.domains, e.g.:\n\n" +
				"  databases:\n" +
				"    primary:\n" +
				"      driver: postgres\n" +
				"      domains:\n" +
				"        auth: [users, sessions]\n\n" +
				"(A top-level `domains:` key is no longer supported; move it under its database.)",
		)
	}

	schemas, err := loadSchemas(cfg.ProjectRoot, cfg.Manifest)
	if err != nil {
		return err
	}

	if len(schemas) == 0 {
		return fmt.Errorf(
			"no reflected schema files found\n\n" +
				"Run 'gopernicus db reflect' first.",
		)
	}

	modulePath, err := project.ModulePath(cfg.ProjectRoot)
	if err != nil {
		return fmt.Errorf("reading module path: %w", err)
	}

	opts := Options{DryRun: cfg.DryRun, Verbose: cfg.Verbose, ForceBootstrap: cfg.ForceBootstrap}

	if cfg.DryRun {
		fmt.Println("=== DRY RUN — no files written ===")
	}

	return runNested(cfg, schemas, modulePath, opts)
}

// injectBridgeAuthSchema overrides the resolved file's auth relations and
// permissions with the entity's bridge.yml auth schema when one exists
// (best-effort: a missing or unparseable bridge.yml leaves the
// queries.sql-derived schema in place). The domain-level auth schema
// generator reads these off the ResolvedFile.
func injectBridgeAuthSchema(resolved *ResolvedFile, projectRoot string) {
	ymlPath := bridgeYMLPath(resolved.DomainName, resolved.TableName, projectRoot)
	if !fileExists(ymlPath) {
		return
	}
	yml, err := ParseBridgeYML(ymlPath)
	if err != nil {
		return
	}
	authEntity := BuildAuthSchemaEntityFromBridgeYML(yml, resolved.TableName)
	if authEntity == nil {
		return
	}

	// Convert back to the AuthRelation/AuthPermission format that the
	// existing auth schema generator expects on ResolvedFile.
	resolved.AuthRelations = nil
	resolved.AuthPermissions = nil
	for _, rel := range authEntity.Relations {
		ar := AuthRelation{Name: rel.Name}
		for _, s := range rel.AllowedSubjects {
			ref := s.Type
			if s.Relation != "" {
				ref += "#" + s.Relation
			}
			ar.Subjects = append(ar.Subjects, ref)
		}
		resolved.AuthRelations = append(resolved.AuthRelations, ar)
	}
	for _, perm := range authEntity.Permissions {
		ap := AuthPermission{Name: perm.Name}
		for _, check := range perm.Checks {
			if check.IsDirect {
				ap.Rules = append(ap.Rules, check.Relation)
			} else {
				ap.Rules = append(ap.Rules, check.Relation+"->"+check.Permission)
			}
		}
		resolved.AuthPermissions = append(resolved.AuthPermissions, ap)
	}
}

// bridgeYMLPath returns the path to an entity's bridge.yml.
func bridgeYMLPath(domainName, tableName, projectRoot string) string {
	return filepath.Join(BridgeDir(domainName, tableName, projectRoot), "bridge.yml")
}

// emitBridgeComposites generates bridge composites and auth schemas for
// domains with bridge routes.
func emitBridgeComposites(
	cfg Config,
	modulePath string,
	authEnabled bool,
	authzProvider manifest.Feature,
	domainBridgeEntities map[string][]BridgeCompositeEntity,
	domainResolvedFiles map[string][]*ResolvedFile,
	opts Options,
) error {
	for domain, bridgeEntities := range domainBridgeEntities {
		compositeDir := BridgeCompositeDir(domain, cfg.ProjectRoot)
		data := BridgeCompositeTemplateData{
			CompositePkg:  BridgeCompositePackage(domain),
			DomainName:    domain,
			ModulePath:    modulePath,
			FrameworkPath: gopernicusFrameworkPath,
			Entities:      bridgeEntities,
			AuthEnabled:   authEnabled,
		}
		fmt.Printf("\n  %s/ (bridge composite)\n", BridgeCompositePackage(domain))
		if err := GenerateBridgeComposite(data, compositeDir, opts); err != nil {
			return fmt.Errorf("bridge composite %s: %w", domain, err)
		}

		// Generate auth schema in the bridge composite directory (auth is a bridge concern).
		if gen, ok := authSchemaRegistry[authzProvider]; ok {
			if err := gen(compositeDir, BridgeCompositePackage(domain), modulePath, domainResolvedFiles[domain], opts); err != nil {
				return fmt.Errorf("auth schema %s: %w", domain, err)
			}
		}
	}
	return nil
}

// emitFixtures writes the cross-domain test fixture packages, one per store
// mode: fixtures/ (pgx, testpgx-backed) and sqlitefixtures/ (spec,
// testsqlite-backed). Multi-homed entities appear in both.
func emitFixtures(pgxEntities, specEntities []FixtureEntity, projectRoot, modulePath string, opts Options) error {
	if len(pgxEntities) > 0 {
		fixtureDir := filepath.Join(projectRoot, "workshop", "testing", "fixtures")
		data := FixtureTemplateData{
			ModulePath:    modulePath,
			FrameworkPath: gopernicusFrameworkPath,
			Entities:      pgxEntities,
		}
		fmt.Printf("\n  fixtures/ (test fixtures)\n")
		if err := GenerateFixtures(data, fixtureDir, opts); err != nil {
			return err
		}
	}
	if len(specEntities) > 0 {
		fixtureDir := filepath.Join(projectRoot, "workshop", "testing", "sqlitefixtures")
		data := FixtureTemplateData{
			ModulePath:    modulePath,
			FrameworkPath: gopernicusFrameworkPath,
			Entities:      specEntities,
		}
		fmt.Printf("\n  sqlitefixtures/ (spec test fixtures)\n")
		if err := GenerateSpecFixtures(data, fixtureDir, opts); err != nil {
			return err
		}
	}
	return nil
}

// generatePgxStoreAndTests generates the pgx store plus its integration
// tests, unless the entity opted out via `-- @skip-integration-test` in
// queries.sql. When skipped, any previously generated test file is removed so
// a stale copy doesn't linger and keep failing. dbName is the manifest
// database hosting the store (locates migrations for the test bootstrap).
func generatePgxStoreAndTests(resolved *ResolvedFile, domainName, modulePath, projectRoot, dbName string, opts Options) error {
	if err := GeneratePgxStore(resolved, domainName, modulePath, projectRoot, opts); err != nil {
		return fmt.Errorf("pgxstore: %w", err)
	}

	storeDir := StoreDir(domainName, resolved.TableName, "pgx", projectRoot)
	if resolved.SkipIntegrationTest {
		stalePath := filepath.Join(storeDir, "generated_test.go")
		if fileExists(stalePath) && !opts.DryRun {
			if err := os.Remove(stalePath); err != nil {
				return fmt.Errorf("remove stale generated_test.go: %w", err)
			}
			if opts.Verbose {
				fmt.Printf("      removed %s (skip-integration-test)\n", stalePath)
			}
		}
		return nil
	}

	testData, err := BuildIntegrationTestData(resolved, modulePath, dbName)
	if err != nil {
		return fmt.Errorf("integration test data: %w", err)
	}
	if err := GenerateIntegrationTest(testData, storeDir, opts); err != nil {
		return fmt.Errorf("integration tests: %w", err)
	}
	return nil
}

// generateSpecStoreTests generates the spec store's integration tests
// (testsqlite + sqlitefixtures), honoring `-- @skip-integration-test` the
// same way the pgx path does.
func generateSpecStoreTests(resolved *ResolvedFile, domainName, modulePath, projectRoot, dbName string, opts Options) error {
	storeDir := StoreDir(domainName, resolved.TableName, specStorePackageSuffix, projectRoot)
	if resolved.SkipIntegrationTest {
		stalePath := filepath.Join(storeDir, "generated_test.go")
		if fileExists(stalePath) && !opts.DryRun {
			if err := os.Remove(stalePath); err != nil {
				return fmt.Errorf("remove stale generated_test.go: %w", err)
			}
			if opts.Verbose {
				fmt.Printf("      removed %s (skip-integration-test)\n", stalePath)
			}
		}
		return nil
	}

	testData, err := BuildIntegrationTestData(resolved, modulePath, dbName)
	if err != nil {
		return fmt.Errorf("spec integration test data: %w", err)
	}
	testData.StorePkg = StorePackage(resolved.TableName, specStorePackageSuffix)
	testData.FixtureImport = modulePath + "/workshop/testing/sqlitefixtures"
	if err := GenerateSpecIntegrationTest(testData, storeDir, opts); err != nil {
		return fmt.Errorf("spec integration tests: %w", err)
	}
	return nil
}

func loadSchemas(root string, m *manifest.Manifest) (map[string]*schema.ReflectedSchema, error) {
	result := make(map[string]*schema.ReflectedSchema)

	dbNames := m.DatabaseNames()
	if len(dbNames) == 0 {
		dbNames = []string{"primary"}
	}

	for _, dbName := range dbNames {
		dbConf := m.DatabaseOrDefault(dbName)
		schemaNames := []string{"public"}
		if dbConf != nil {
			schemaNames = dbConf.SchemasOrDefault()
		}

		for _, schemaName := range schemaNames {
			jsonPath := filepath.Join(root, manifest.MigrationsDir(dbName), "_"+schemaName+".json")
			s, err := schema.LoadJSON(jsonPath)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, fmt.Errorf("loading %s: %w", jsonPath, err)
			}
			key := dbName + ":" + schemaName
			result[key] = s
		}
	}

	return result, nil
}

func inferTableName(dirName string, schemas map[string]*schema.ReflectedSchema, dbName string) (tableName, schemaName string, err error) {
	for key, s := range schemas {
		if !strings.HasPrefix(key, dbName+":") {
			continue
		}
		for name := range s.Tables {
			if ToPackageName(name) == dirName {
				parts := strings.SplitN(key, ":", 2)
				return name, parts[1], nil
			}
		}
	}
	return "", "", fmt.Errorf("no table found matching directory %q in database %q", dirName, dbName)
}
