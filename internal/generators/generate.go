package generators

import (
	"fmt"
	"go/format"
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

// Run executes code generation by scanning for queries.sql files and
// cross-referencing them with reflected schema.
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

	modulePath, err := project.ModulePath(cfg.ProjectRoot)
	if err != nil {
		return fmt.Errorf("reading module path: %w", err)
	}

	opts := Options{DryRun: cfg.DryRun, Verbose: cfg.Verbose, ForceBootstrap: cfg.ForceBootstrap}

	if cfg.DryRun {
		fmt.Println("=== DRY RUN — no files written ===")
	}

	// Collect entities per domain for composite generation.
	domainEntities := make(map[string][]CompositeEntity)
	domainBridgeEntities := make(map[string][]BridgeCompositeEntity) // entities with @http:json routes
	var allFixtureEntities []FixtureEntity // entities for fixture generation (single package, cross-domain)
	var allE2ETestData []*E2ETestData      // E2E test data for entities with HTTP routes
	domainDirs := make(map[string]string)                            // domain name → absolute dir path
	domainTableNames := make(map[string]map[string]string)           // domain → (pkgName → tableName)
	domainResolvedFiles := make(map[string][]*ResolvedFile)          // domain → resolved files (for auth schema)

	authEnabled := cfg.Manifest.Features.AuthenticationEnabled()
	authzProvider := cfg.Manifest.Features.AuthorizationProvider()

	for _, qfPath := range queryFiles {
		resolved, err := generateFromQueryFile(qfPath, schemas, modulePath, cfg.ProjectRoot, authEnabled, opts)
		if err != nil {
			return fmt.Errorf("%s: %w", qfPath, err)
		}
		if resolved == nil {
			continue // skipped (no matching table)
		}

		// Inject auth schema from bridge.yml into resolved file (for domain-level auth schema generation).
		bridgeDir := BridgeDir(resolved.DomainName, resolved.TableName, cfg.ProjectRoot)
		ymlPath := filepath.Join(bridgeDir, "bridge.yml")
		if fileExists(ymlPath) {
			yml, err := ParseBridgeYML(ymlPath)
			if err == nil {
				authEntity := BuildAuthSchemaEntityFromBridgeYML(yml, resolved.TableName)
				if authEntity != nil {
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
			}
		}

		domain := resolved.DomainName
		if domain != "" {
			domainEntities[domain] = append(domainEntities[domain], BuildCompositeEntity(resolved))
			if _, ok := domainDirs[domain]; !ok {
				domainDirs[domain] = filepath.Join(repoRoot, domain)
			}
			if domainTableNames[domain] == nil {
				domainTableNames[domain] = make(map[string]string)
			}
			domainTableNames[domain][resolved.PackageName] = resolved.TableName
			domainResolvedFiles[domain] = append(domainResolvedFiles[domain], resolved)

			// Track entities that have bridge routes for bridge composite generation.
			// In flat mode, check for bridge.yml existence instead of @http:json annotations.
			hasBridge := resolvedHasBridgeRoutes(resolved) || fileExists(ymlPath)
			if hasBridge {
				domainBridgeEntities[domain] = append(domainBridgeEntities[domain], BuildBridgeCompositeEntity(resolved))
			}

			// Track entities for fixture generation.
			allFixtureEntities = append(allFixtureEntities, BuildFixtureEntity(resolved, modulePath))

			// Track entities with HTTP routes for E2E test generation.
			if resolvedHasBridgeRoutes(resolved) {
				e2eData, err := BuildE2ETestData(resolved, modulePath)
				if err != nil {
					return fmt.Errorf("%s: e2e test data: %w", resolved.TableName, err)
				}
				allE2ETestData = append(allE2ETestData, e2eData)
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
			FrameworkPath: goperniculusFrameworkPath,
			DomainPath:    "core/repositories/" + domain,
			Entities:      entities,
			HasEvents:     true, // always available — custom methods may need the event bus
			HasAuth:       hasAuthProvider,
		}
		fmt.Printf("\n  %s/ (domain composite)\n", domain)
		if err := GenerateComposite(data, domainDir, opts); err != nil {
			return fmt.Errorf("composite %s: %w", domain, err)
		}

	}

	// Generate bridge composites and auth schemas for domains with bridge routes.
	for domain, bridgeEntities := range domainBridgeEntities {
		compositeDir := BridgeCompositeDir(domain, cfg.ProjectRoot)
		data := BridgeCompositeTemplateData{
			CompositePkg:  BridgeCompositePackage(domain),
			DomainName:    domain,
			ModulePath:    modulePath,
			FrameworkPath: goperniculusFrameworkPath,
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

	// Generate test fixtures (single package across all domains for cross-domain FK resolution).
	if len(allFixtureEntities) > 0 {
		fixtureDir := filepath.Join(cfg.ProjectRoot, "workshop", "testing", "fixtures")
		data := FixtureTemplateData{
			ModulePath:    modulePath,
			FrameworkPath: goperniculusFrameworkPath,
			Entities:      allFixtureEntities,
		}
		fmt.Printf("\n  fixtures/ (test fixtures)\n")
		if err := GenerateFixtures(data, fixtureDir, opts); err != nil {
			return fmt.Errorf("fixtures: %w", err)
		}
	}

	// Generate E2E tests (single package, one test file per entity).
	if len(allE2ETestData) > 0 {
		e2eDir := filepath.Join(cfg.ProjectRoot, "workshop", "testing", "e2e")
		fmt.Printf("\n  e2e/ (E2E tests)\n")

		// Generate bootstrap setup file once.
		bootstrapData := allE2ETestData[0] // use first entity for module path info
		bootstrapPath := filepath.Join(e2eDir, "setup_test.go")
		if !fileExists(bootstrapPath) || opts.ForceBootstrap {
			out, err := renderE2ETestTemplate(e2eTestBootstrapTemplate, bootstrapData)
			if err != nil {
				return fmt.Errorf("render setup_test.go: %w", err)
			}
			formatted, fmtErr := format.Source(out)
			if fmtErr != nil {
				formatted = out
			}
			if err := writeFile(bootstrapPath, formatted, opts); err != nil {
				return fmt.Errorf("write setup_test.go: %w", err)
			}
		}

		// Generate per-entity test files.
		for _, data := range allE2ETestData {
			if err := GenerateE2ETest(data, e2eDir, opts); err != nil {
				return fmt.Errorf("e2e %s: %w", data.EntityName, err)
			}
		}
	}

	return nil
}

// resolvedHasBridgeRoutes returns true if any query in the resolved file has
// annotation-based @http:json routes. Always returns false now that protocol
// config has moved to bridge.yml; bridge.yml existence is checked separately.
func resolvedHasBridgeRoutes(_ *ResolvedFile) bool {
	return false
}

func generateFromQueryFile(
	qfPath string,
	schemas map[string]*schema.ReflectedSchema,
	modulePath, projectRoot string,
	authEnabled bool,
	opts Options,
) (*ResolvedFile, error) {
	qf, err := Parse(qfPath)
	if err != nil {
		return nil, err
	}

	repoDir := filepath.Dir(qfPath)
	dirName := filepath.Base(repoDir)

	tableName, schemaName, err := inferTableName(dirName, schemas, qf.Database)
	if err != nil {
		if opts.Verbose {
			fmt.Printf("  skip %s (no matching table in schema)\n", dirName)
		}
		return nil, nil
	}
	qf.Table = tableName

	key := qf.Database + ":" + schemaName
	s, ok := schemas[key]
	if !ok {
		return nil, fmt.Errorf(
			"reflected schema for database %q schema %q not found\n\n"+
				"Run 'gopernicus db reflect' to generate it.",
			qf.Database, schemaName,
		)
	}

	domainName := domainFromPath(qfPath, projectRoot)

	resolved, err := Resolve(qf, s, domainName)
	if err != nil {
		return nil, err
	}

	fmt.Printf("\n  %s (table: %s)\n", filepath.Base(repoDir), resolved.TableName)

	// Generate repository layer.
	if err := GenerateRepository(resolved, repoDir, opts); err != nil {
		return nil, fmt.Errorf("repository: %w", err)
	}

	// Generate pgxstore layer.
	if err := GeneratePgxStore(resolved, domainName, modulePath, projectRoot, opts); err != nil {
		return nil, fmt.Errorf("pgxstore: %w", err)
	}

	// Generate pgxstore integration tests.
	storeDir := StoreDir(domainName, resolved.TableName, "pgx", projectRoot)
	testData, err := BuildIntegrationTestData(resolved, modulePath)
	if err != nil {
		return nil, fmt.Errorf("integration test data: %w", err)
	}
	if err := GenerateIntegrationTest(testData, storeDir, opts); err != nil {
		return nil, fmt.Errorf("integration tests: %w", err)
	}

	// Generate cache layer (only if any @cache annotations exist).
	if generated, err := GenerateCache(resolved, repoDir, opts); err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	} else if generated && opts.Verbose {
		fmt.Printf("    generated cache layer\n")
	}

	// Generate bridge layer (from bridge.yml).
	if generated, err := GenerateBridge(resolved, domainName, modulePath, projectRoot, authEnabled, opts); err != nil {
		return nil, fmt.Errorf("bridge: %w", err)
	} else if generated && opts.Verbose {
		fmt.Printf("    generated bridge layer\n")
	}

	return resolved, nil
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
