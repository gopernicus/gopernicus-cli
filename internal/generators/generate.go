package generators

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// Run executes code generation, dispatching on the manifest's domain shape:
// when any database declares domains (the nested shape,
// databases.<name>.domains), the manifest is the sole binding source and
// generation iterates database×domain×entity; otherwise the legacy path
// scans for queries.sql files and binds each via its @database: annotation.
func Run(cfg Config) error {
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

	if cfg.Manifest.NestedDomainsDeclared() {
		return runNested(cfg, schemas, modulePath, opts)
	}
	return runLegacy(cfg, schemas, modulePath, opts)
}

// runLegacy is the discovery-driven path: every queries.sql under
// core/repositories/ is generated, bound to a database by its @database:
// annotation (default "primary").
func runLegacy(cfg Config, schemas map[string]*schema.ReflectedSchema, modulePath string, opts Options) error {
	repoRoot := filepath.Join(cfg.ProjectRoot, "core", "repositories")
	queryFiles, err := discoverQueryFiles(repoRoot, cfg.Domain)
	if err != nil {
		return err
	}

	if len(queryFiles) == 0 {
		return fmt.Errorf(
			"no queries.sql files found under %s\n\n"+
				"Run 'gopernicus new repo <domain/entity>' to scaffold a repository.",
			repoRoot,
		)
	}

	// Collect entities per domain for composite generation.
	domainEntities := make(map[string][]CompositeEntity)
	domainBridgeEntities := make(map[string][]BridgeCompositeEntity) // entities with a bridge.yml
	var pgxFixtureEntities, specFixtureEntities []FixtureEntity      // entities for fixture generation (cross-domain, per store mode)
	domainDirs := make(map[string]string)                            // domain name → absolute dir path
	domainTableNames := make(map[string]map[string]string)           // domain → (pkgName → tableName)
	domainResolvedFiles := make(map[string][]*ResolvedFile)          // domain → resolved files (for auth schema)

	authEnabled := cfg.Manifest.Features.AuthenticationEnabled()
	authzProvider := cfg.Manifest.Features.AuthorizationProvider()

	domainStoreModes := make(map[string]manifest.StoreMode) // domain → resolved store mode

	for _, qfPath := range queryFiles {
		resolved, storeMode, err := generateFromQueryFile(qfPath, schemas, cfg.Manifest, modulePath, cfg.ProjectRoot, authEnabled, opts)
		if err != nil {
			return fmt.Errorf("%s: %w", qfPath, err)
		}
		if resolved == nil {
			continue // skipped (no matching table)
		}

		// Inject auth schema from bridge.yml into resolved file (for domain-level auth schema generation).
		injectBridgeAuthSchema(resolved, cfg.ProjectRoot)
		ymlPath := bridgeYMLPath(resolved.DomainName, resolved.TableName, cfg.ProjectRoot)

		domain := resolved.DomainName
		if domain != "" {
			if prev, ok := domainStoreModes[domain]; ok && prev != storeMode {
				return fmt.Errorf(
					"domain %q mixes store modes (%s and %s); all entities in a domain must share one store mode",
					domain, prev, storeMode,
				)
			}
			domainStoreModes[domain] = storeMode

			domainEntities[domain] = append(domainEntities[domain], BuildCompositeEntity(resolved))
			if _, ok := domainDirs[domain]; !ok {
				domainDirs[domain] = filepath.Join(repoRoot, domain)
			}
			if domainTableNames[domain] == nil {
				domainTableNames[domain] = make(map[string]string)
			}
			domainTableNames[domain][resolved.PackageName] = resolved.TableName
			domainResolvedFiles[domain] = append(domainResolvedFiles[domain], resolved)

			// Track entities that have a bridge.yml for bridge composite generation.
			if fileExists(ymlPath) {
				domainBridgeEntities[domain] = append(domainBridgeEntities[domain], BuildBridgeCompositeEntity(resolved))
			}

			// Track entities for fixture generation, per store mode.
			if storeMode == manifest.StoreModeSpec {
				specFixtureEntities = append(specFixtureEntities, BuildFixtureEntity(resolved, modulePath))
			} else {
				pgxFixtureEntities = append(pgxFixtureEntities, BuildFixtureEntity(resolved, modulePath))
			}
		}
	}

	// Generate domain composites and auth schemas.
	for domain, entities := range domainEntities {
		domainDir := domainDirs[domain]
		_, hasAuthProvider := authSchemaRegistry[authzProvider]

		data := CompositeTemplateData{
			DomainPkg:     domain,
			ModulePath:    modulePath,
			FrameworkPath: gopernicusFrameworkPath,
			DomainPath:    "core/repositories/" + domain,
			Entities:      entities,
			HasEvents:     true, // always available — custom methods may need the event bus
			HasAuth:       hasAuthProvider,
			SpecMode:      domainStoreModes[domain] == manifest.StoreModeSpec,
		}
		fmt.Printf("\n  %s/ (domain composite)\n", domain)
		if err := GenerateComposite(data, domainDir, opts); err != nil {
			return fmt.Errorf("composite %s: %w", domain, err)
		}

	}

	// Generate bridge composites and auth schemas for domains with bridge routes.
	if err := emitBridgeComposites(cfg, modulePath, authEnabled, authzProvider, domainBridgeEntities, domainResolvedFiles, opts); err != nil {
		return err
	}

	// Generate test fixtures. The fixtures file is a single package across ALL
	// domains so cross-domain FK chains resolve (e.g. a graph entity referencing
	// a worlds entity needs the worlds fixture in scope). When a domain filter
	// is active, augment the in-scope entities with parse+resolve of every other
	// domain's queries.sql so we don't overwrite the file with only the filtered
	// domain's entities.
	if cfg.Domain != "" {
		inScope := make(map[string]bool, len(queryFiles))
		for _, qf := range queryFiles {
			inScope[qf] = true
		}
		allQueryFiles, err := discoverQueryFiles(repoRoot, "")
		if err != nil {
			return fmt.Errorf("discover all queries for fixtures: %w", err)
		}
		for _, qfPath := range allQueryFiles {
			if inScope[qfPath] {
				continue
			}
			resolved, dbName, err := resolveForFixture(qfPath, schemas, cfg.ProjectRoot)
			if err != nil {
				return fmt.Errorf("%s: resolve for fixture: %w", qfPath, err)
			}
			if resolved == nil {
				continue
			}
			mode, err := cfg.Manifest.DatabaseOrDefault(dbName).StoreMode()
			if err != nil {
				return fmt.Errorf("%s: store mode: %w", qfPath, err)
			}
			if mode == manifest.StoreModeSpec {
				specFixtureEntities = append(specFixtureEntities, BuildFixtureEntity(resolved, modulePath))
			} else {
				pgxFixtureEntities = append(pgxFixtureEntities, BuildFixtureEntity(resolved, modulePath))
			}
		}
	}
	return emitFixtures(pgxFixtureEntities, specFixtureEntities, cfg.ProjectRoot, modulePath, opts)
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

func generateFromQueryFile(
	qfPath string,
	schemas map[string]*schema.ReflectedSchema,
	m *manifest.Manifest,
	modulePath, projectRoot string,
	authEnabled bool,
	opts Options,
) (*ResolvedFile, manifest.StoreMode, error) {
	qf, err := Parse(qfPath)
	if err != nil {
		return nil, "", err
	}

	storeMode, err := m.DatabaseOrDefault(qf.Database).StoreMode()
	if err != nil {
		return nil, "", fmt.Errorf("database %q: %w", qf.Database, err)
	}

	repoDir := filepath.Dir(qfPath)
	dirName := filepath.Base(repoDir)

	tableName, schemaName, err := inferTableName(dirName, schemas, qf.Database)
	if err != nil {
		if opts.Verbose {
			fmt.Printf("  skip %s (no matching table in schema)\n", dirName)
		}
		return nil, "", nil
	}
	qf.Table = tableName

	key := qf.Database + ":" + schemaName
	s, ok := schemas[key]
	if !ok {
		return nil, "", fmt.Errorf(
			"reflected schema for database %q schema %q not found\n\n"+
				"Run 'gopernicus db reflect' to generate it.",
			qf.Database, schemaName,
		)
	}

	domainName := domainFromPath(qfPath, projectRoot)

	resolved, err := Resolve(qf, s, domainName)
	if err != nil {
		return nil, "", err
	}

	fmt.Printf("\n  %s (table: %s)\n", filepath.Base(repoDir), resolved.TableName)

	// Generate repository layer.
	if err := GenerateRepository(resolved, repoDir, opts); err != nil {
		return nil, "", fmt.Errorf("repository: %w", err)
	}

	// Generate the store layer per the database's store mode.
	switch storeMode {
	case manifest.StoreModeSpec:
		// Composite wiring imports the spec store package, not the pgx one.
		resolved.StorePkg = StorePackage(resolved.TableName, specStorePackageSuffix)

		if err := GenerateSpecStore(resolved, repoDir, modulePath, opts); err != nil {
			return nil, "", fmt.Errorf("specstore: %w", err)
		}

		// Integration test generation is pgx-coupled (testcontainers + pgx
		// fixtures) and has no spec-mode equivalent yet.
		fmt.Printf("      note: integration test generation is pgx-only — skipped in spec store mode\n")

	default: // manifest.StoreModePgx
		if err := generatePgxStoreAndTests(resolved, domainName, modulePath, projectRoot, qf.Database, opts); err != nil {
			return nil, "", err
		}
	}

	// Generate cache layer (only if any @cache annotations exist).
	if generated, err := GenerateCache(resolved, repoDir, false, opts); err != nil {
		return nil, "", fmt.Errorf("cache: %w", err)
	} else if generated && opts.Verbose {
		fmt.Printf("    generated cache layer\n")
	}

	// Generate bridge layer (from bridge.yml).
	if generated, err := GenerateBridge(resolved, domainName, modulePath, projectRoot, authEnabled, opts); err != nil {
		return nil, "", fmt.Errorf("bridge: %w", err)
	} else if generated && opts.Verbose {
		fmt.Printf("    generated bridge layer\n")
	}

	return resolved, storeMode, nil
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

// resolveForFixture parses and resolves a queries.sql file without running
// any code generation. Used to collect FixtureEntity data for entities that
// live outside the current `--domain` filter, so the fixtures package can stay
// cumulative across domains. Returns (nil, nil) if the table cannot be
// matched against any reflected schema (same skip semantics as
// generateFromQueryFile).
func resolveForFixture(
	qfPath string,
	schemas map[string]*schema.ReflectedSchema,
	projectRoot string,
) (*ResolvedFile, string, error) {
	qf, err := Parse(qfPath)
	if err != nil {
		return nil, "", err
	}

	repoDir := filepath.Dir(qfPath)
	dirName := filepath.Base(repoDir)

	tableName, schemaName, err := inferTableName(dirName, schemas, qf.Database)
	if err != nil {
		return nil, "", nil
	}
	qf.Table = tableName

	key := qf.Database + ":" + schemaName
	s, ok := schemas[key]
	if !ok {
		return nil, "", fmt.Errorf(
			"reflected schema for database %q schema %q not found\n\n"+
				"Run 'gopernicus db reflect' to generate it.",
			qf.Database, schemaName,
		)
	}

	domainName := domainFromPath(qfPath, projectRoot)
	resolved, err := Resolve(qf, s, domainName)
	return resolved, qf.Database, err
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

func discoverQueryFiles(repoRoot, domainFilter string) ([]string, error) {
	var result []string

	searchRoot := repoRoot
	if domainFilter != "" {
		searchRoot = filepath.Join(repoRoot, domainFilter)
	}

	err := filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() && info.Name() == "queries.sql" {
			result = append(result, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(result)
	return result, nil
}

func domainFromPath(qfPath, projectRoot string) string {
	repoDir := filepath.Dir(qfPath)
	parent := filepath.Dir(repoDir)
	repoRoot := filepath.Join(projectRoot, "core", "repositories")

	if parent == repoRoot {
		return ""
	}

	rel, err := filepath.Rel(repoRoot, parent)
	if err != nil || rel == "." {
		return ""
	}
	parts := strings.SplitN(rel, string(filepath.Separator), 2)
	return parts[0]
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
