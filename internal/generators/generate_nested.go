package generators

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// entityBinding is one entity declared under the nested manifest shape
// (databases.<name>.domains), flattened across every database that hosts it.
// An entity is identified by (domain, entity); the same table name under two
// different domains is two distinct entities with two distinct repositories.
type entityBinding struct {
	Domain  string
	Table   string   // manifest entry, e.g. "event_outbox"
	PkgName string   // repo directory / package name, e.g. "eventoutbox"
	DBs     []string // hosting databases in canonical order; DBs[0] is the canonical database
}

// dbDomainKey addresses the composite for one domain in one database.
type dbDomainKey struct {
	DB     string
	Domain string
}

// nestedBindings flattens databases×domains×entities into a deterministic
// binding list: databases iterate primary-first then sorted, domains and
// tables sorted. The first database that declares an entity becomes its
// canonical database (DBs[0]).
func nestedBindings(m *manifest.Manifest) []*entityBinding {
	var bindings []*entityBinding
	index := make(map[string]*entityBinding)

	for _, db := range m.DatabaseNamesPrimaryFirst() {
		conf := m.Databases[db]
		if conf == nil {
			continue
		}
		domains := make([]string, 0, len(conf.Domains))
		for domain := range conf.Domains {
			domains = append(domains, domain)
		}
		sort.Strings(domains)

		for _, domain := range domains {
			tables := append([]string(nil), conf.Domains[domain]...)
			sort.Strings(tables)
			for _, table := range tables {
				key := domain + "/" + ToPackageName(table)
				b := index[key]
				if b == nil {
					b = &entityBinding{Domain: domain, Table: table, PkgName: ToPackageName(table)}
					index[key] = b
					bindings = append(bindings, b)
				}
				hosted := false
				for _, existing := range b.DBs {
					if existing == db {
						hosted = true
						break
					}
				}
				if !hosted {
					b.DBs = append(b.DBs, db)
				}
			}
		}
	}
	return bindings
}

// runNested is the manifest-driven path for the nested domain shape:
// databases.<name>.domains is the sole binding source and generation iterates
// database×domain×entity in canonical order ("primary" first, then sorted).
//
// Semantics:
//
//   - Canonical snapshot: the first database declaring an entity is its
//     canonical database; its reflected schema snapshot drives everything
//     generated for the entity (entity package, cache, bridge, fixtures, and
//     all store packages). Secondary databases' snapshots are not consulted.
//   - Store dedupe: stores generate once per (entity × store mode) — an
//     entity hosted by N spec-mode databases gets one <entity>store package;
//     an entity hosted by both a pgx-mode and a spec-mode database gets both
//     sibling packages.
//   - Composites: per (database, domain). A domain hosted by one database
//     keeps the classic generated_composite.go; multiple hosts get one
//     generated_composite_<db>.go (NewRepositories<Db>) per database plus a
//     shared types file.
//   - Cache keys: entities hosted by >1 database get a database-qualified
//     cache key prefix per composite ("<db>:<domain>:<table>"); single-homed
//     entities keep the default prefix.
func runNested(cfg Config, schemas map[string]*schema.ReflectedSchema, modulePath string, opts Options) error {
	m := cfg.Manifest
	repoRoot := filepath.Join(cfg.ProjectRoot, "core", "repositories")

	for _, w := range m.DomainShapeWarnings() {
		fmt.Printf("  warning: %s\n", w)
	}

	// Validate every database's store mode up front so configuration errors
	// surface before any file is written.
	dbModes := make(map[string]manifest.StoreMode)
	for _, db := range m.DatabaseNamesPrimaryFirst() {
		mode, err := m.Databases[db].StoreMode()
		if err != nil {
			return fmt.Errorf("database %q: %w", db, err)
		}
		dbModes[db] = mode
	}

	bindings := nestedBindings(m)

	if cfg.Domain != "" {
		found := false
		for _, b := range bindings {
			if b.Domain == cfg.Domain {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf(
				"domain %q is not declared under any database in gopernicus.yml", cfg.Domain,
			)
		}
	}

	authEnabled := m.Features.AuthenticationEnabled()
	authzProvider := m.Features.AuthorizationProvider()
	_, hasAuthProvider := authSchemaRegistry[authzProvider]

	dbDomainEntities := make(map[dbDomainKey][]CompositeEntity)
	domainDBs := make(map[string][]string) // domain → hosting dbs, canonical order
	domainBridgeEntities := make(map[string][]BridgeCompositeEntity)
	domainResolvedFiles := make(map[string][]*ResolvedFile)
	var pgxFixtureEntities, specFixtureEntities []FixtureEntity

	for _, b := range bindings {
		if cfg.Domain != "" && b.Domain != cfg.Domain {
			continue
		}

		repoDir := filepath.Join(repoRoot, b.Domain, b.PkgName)
		resolved, err := resolveNestedBinding(b, repoDir, schemas, true)
		if err != nil {
			return err
		}
		if resolved == nil {
			continue // table not in the canonical snapshot — skip note already printed
		}

		fmt.Printf("\n  %s (table: %s, databases: %s)\n",
			b.PkgName, resolved.TableName, strings.Join(b.DBs, ", "))

		multiHomed := len(b.DBs) > 1

		// Entity package — generated once, from the canonical snapshot.
		if err := GenerateRepository(resolved, repoDir, opts); err != nil {
			return fmt.Errorf("%s/%s: repository: %w", b.Domain, b.PkgName, err)
		}
		if generated, err := GenerateCache(resolved, repoDir, multiHomed, opts); err != nil {
			return fmt.Errorf("%s/%s: cache: %w", b.Domain, b.PkgName, err)
		} else if generated && opts.Verbose {
			fmt.Printf("    generated cache layer\n")
		}
		// e2e boots against the canonical hosting database (DBs[0]) in its
		// store mode.
		hostDB := ""
		hostSpecMode := false
		if len(b.DBs) > 0 {
			hostDB = b.DBs[0]
			hostSpecMode = dbModes[hostDB] == manifest.StoreModeSpec
		}
		if generated, err := GenerateBridge(resolved, b.Domain, modulePath, cfg.ProjectRoot, authEnabled, hostDB, hostSpecMode, opts); err != nil {
			return fmt.Errorf("%s/%s: bridge: %w", b.Domain, b.PkgName, err)
		} else if generated && opts.Verbose {
			fmt.Printf("    generated bridge layer\n")
		}

		// Stores — once per (entity × store mode), all from the canonical snapshot.
		seenModes := make(map[manifest.StoreMode]bool)
		for _, db := range b.DBs {
			mode := dbModes[db]
			if seenModes[mode] {
				continue
			}
			seenModes[mode] = true

			switch mode {
			case manifest.StoreModeSpec:
				if err := GenerateSpecStore(resolved, repoDir, modulePath, opts); err != nil {
					return fmt.Errorf("%s/%s: specstore: %w", b.Domain, b.PkgName, err)
				}
				if err := generateSpecStoreTests(resolved, b.Domain, modulePath, cfg.ProjectRoot, db, opts); err != nil {
					return fmt.Errorf("%s/%s: %w", b.Domain, b.PkgName, err)
				}
			default: // manifest.StoreModePgx
				if err := generatePgxStoreAndTests(resolved, b.Domain, modulePath, cfg.ProjectRoot, db, opts); err != nil {
					return fmt.Errorf("%s/%s: %w", b.Domain, b.PkgName, err)
				}
			}
		}

		// Composite wiring — one entry per hosting database.
		for _, db := range b.DBs {
			suffix := "pgx"
			if dbModes[db] == manifest.StoreModeSpec {
				suffix = specStorePackageSuffix
			}
			entity := CompositeEntity{
				FieldName:  resolved.EntityName,
				RepoPkg:    resolved.PackageName,
				StorePkg:   StorePackage(resolved.TableName, suffix),
				EntityName: resolved.EntityName,
				HasEvents:  true, // always available — custom methods may need the event bus
			}
			if multiHomed {
				entity.CacheKeyPrefix = db + ":" + b.Domain + ":" + resolved.TableName
			}
			key := dbDomainKey{DB: db, Domain: b.Domain}
			dbDomainEntities[key] = append(dbDomainEntities[key], entity)

			hosted := false
			for _, existing := range domainDBs[b.Domain] {
				if existing == db {
					hosted = true
					break
				}
			}
			if !hosted {
				domainDBs[b.Domain] = append(domainDBs[b.Domain], db)
			}
		}

		// Auth schema, bridge composite, and fixture tracking (as in the legacy path).
		injectBridgeAuthSchema(resolved, cfg.ProjectRoot)
		domainResolvedFiles[b.Domain] = append(domainResolvedFiles[b.Domain], resolved)
		if fileExists(bridgeYMLPath(b.Domain, resolved.TableName, cfg.ProjectRoot)) {
			domainBridgeEntities[b.Domain] = append(domainBridgeEntities[b.Domain], BuildBridgeCompositeEntity(resolved))
		}
		fixtureEntity := BuildFixtureEntity(resolved, modulePath)
		for _, mode := range hostedStoreModes(b.DBs, dbModes) {
			if mode == manifest.StoreModeSpec {
				specFixtureEntities = append(specFixtureEntities, fixtureEntity)
			} else {
				pgxFixtureEntities = append(pgxFixtureEntities, fixtureEntity)
			}
		}
	}

	// Domain composites, per (database, domain).
	domains := make([]string, 0, len(domainDBs))
	for domain := range domainDBs {
		domains = append(domains, domain)
	}
	sort.Strings(domains)

	for _, domain := range domains {
		var composites []DBComposite
		for _, db := range domainDBs[domain] {
			composites = append(composites, DBComposite{
				DBName: db,
				Data: CompositeTemplateData{
					DomainPkg:     domain,
					ModulePath:    modulePath,
					FrameworkPath: gopernicusFrameworkPath,
					DomainPath:    "core/repositories/" + domain,
					Entities:      dbDomainEntities[dbDomainKey{DB: db, Domain: domain}],
					HasEvents:     true, // always available — custom methods may need the event bus
					HasAuth:       hasAuthProvider,
					SpecMode:      dbModes[db] == manifest.StoreModeSpec,
				},
			})
		}
		fmt.Printf("\n  %s/ (domain composite)\n", domain)
		if err := GenerateDomainComposites(filepath.Join(repoRoot, domain), composites, opts); err != nil {
			return fmt.Errorf("composite %s: %w", domain, err)
		}
	}

	// Bridge composites and auth schemas.
	if err := emitBridgeComposites(cfg, modulePath, authEnabled, authzProvider, domainBridgeEntities, domainResolvedFiles, opts); err != nil {
		return err
	}

	// Test fixtures are a single cross-domain package: when a domain filter is
	// active, resolve the out-of-scope bindings too so the fixtures file isn't
	// overwritten with only the filtered domain's entities.
	if cfg.Domain != "" {
		for _, b := range bindings {
			if b.Domain == cfg.Domain {
				continue
			}
			repoDir := filepath.Join(repoRoot, b.Domain, b.PkgName)
			resolved, err := resolveNestedBinding(b, repoDir, schemas, false)
			if err != nil || resolved == nil {
				continue // out-of-scope entities are best-effort for fixtures
			}
			fixtureEntity := BuildFixtureEntity(resolved, modulePath)
			for _, mode := range hostedStoreModes(b.DBs, dbModes) {
				if mode == manifest.StoreModeSpec {
					specFixtureEntities = append(specFixtureEntities, fixtureEntity)
				} else {
					pgxFixtureEntities = append(pgxFixtureEntities, fixtureEntity)
				}
			}
		}
	}
	return emitFixtures(pgxFixtureEntities, specFixtureEntities, cfg.ProjectRoot, modulePath, opts)
}

// hostedStoreModes returns the distinct store modes of the databases hosting
// an entity, in pgx-then-spec order — a multi-homed entity may need fixtures
// for both packages.
func hostedStoreModes(dbs []string, dbModes map[string]manifest.StoreMode) []manifest.StoreMode {
	var modes []manifest.StoreMode
	seen := make(map[manifest.StoreMode]bool, 2)
	for _, db := range dbs {
		mode := dbModes[db]
		if !seen[mode] {
			seen[mode] = true
			modes = append(modes, mode)
		}
	}
	return modes
}

// resolveNestedBinding parses and resolves a binding's queries.sql against
// its canonical database's schema snapshot. When report is true, a missing
// queries.sql is a hard error (the manifest explicitly declares the entity)
// and skip notes/warnings are printed; when false, problems return (nil, nil)
// or the parse/resolve error silently (used for out-of-scope fixture
// collection).
func resolveNestedBinding(
	b *entityBinding,
	repoDir string,
	schemas map[string]*schema.ReflectedSchema,
	report bool,
) (*ResolvedFile, error) {
	qfPath := filepath.Join(repoDir, "queries.sql")
	if !fileExists(qfPath) {
		if !report {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"%s/%s is declared under database %q in gopernicus.yml but %s does not exist\n\n"+
				"Run 'gopernicus new repo %s/%s' to scaffold it.",
			b.Domain, b.PkgName, b.DBs[0], qfPath, b.Domain, b.Table,
		)
	}

	qf, err := Parse(qfPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", qfPath, err)
	}
	if report && qf.DatabaseExplicit {
		fmt.Printf("  warning: %s: '-- @database: %s' is ignored — databases.<name>.domains in gopernicus.yml is the binding source\n",
			qfPath, qf.Database)
	}

	canonical := b.DBs[0]
	tableName, schemaName, err := inferTableName(b.PkgName, schemas, canonical)
	if err != nil {
		if report {
			fmt.Printf("  skip %s/%s (table not found in reflected schema for database %q — run 'gopernicus db reflect')\n",
				b.Domain, b.PkgName, canonical)
		}
		return nil, nil
	}
	qf.Table = tableName

	resolved, err := Resolve(qf, schemas[canonical+":"+schemaName], b.Domain)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", qfPath, err)
	}
	return resolved, nil
}
