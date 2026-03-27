package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/fwsource"
	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/project"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

var newCmd = &Command{
	Name:  "new",
	Short: "Scaffold new project components",
	Long:  "Scaffold new project components (repositories, etc.).",
	Usage: "gopernicus new <subcommand>",
}

func init() {
	newCmd.SubCommands = []*Command{
		{
			Name:  "repo",
			Short: "Scaffold a new repository from reflected schema",
			Long: `Scaffold a new repository with queries.sql.

The argument is domain/entity (e.g. "auth/users"). The entity name is used to
look up the table in the reflected schema. Use --table to override the table name.

Creates the repo directory and a queries.sql file with default CRUD operations.
Run 'gopernicus generate' to produce Go code from the queries.

To bootstrap all repos for a domain, use 'gopernicus boot repos <domain>'.

Examples:
  gopernicus new repo auth/users                    # single repo
  gopernicus new repo auth/users --table user_accts # override table name`,
			Usage: "gopernicus new repo <domain/entity> [--db <name>] [--table <name>]",
			Run:   runNewRepo,
		},
		{
			Name:  "case",
			Short: "Scaffold a new use case with bridge",
			Long: `Scaffold a new use case (case) with core logic and HTTP bridge.

Creates both the core case package and its HTTP bridge:
  core/cases/<name>/       — business logic (case.go, errors.go, events.go)
  bridge/cases/<name>bridge/ — HTTP handlers (bridge.go, http.go, model.go)

Case routes register under /cases/<kebab-name>/ to avoid conflicts with
generated CRUD routes.

Examples:
  gopernicus new case tenantadmin     # creates tenantadmin case + bridge
  gopernicus new case audiorecorder   # creates audiorecorder case + bridge`,
			Usage: "gopernicus new case <name>",
			Run:   runNewCase,
		},
	}
	newCmd.Run = runNew
	RegisterCommand(newCmd)
}

func runNew(_ context.Context, args []string) error {
	if len(args) == 0 {
		printCommandHelp(newCmd)
		return nil
	}
	name := args[0]
	for _, sub := range newCmd.SubCommands {
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
	return fmt.Errorf("unknown new subcommand %q\n\nRun 'gopernicus new --help' for usage.", name)
}

func runNewRepo(_ context.Context, args []string) error {
	dbName := flagValue(args, "--db")
	if dbName == "" {
		dbName = "primary"
	}
	tableOverride := flagValue(args, "--table")

	var entityArg string
	for i := 0; i < len(args); i++ {
		if args[i] == "--db" || args[i] == "--table" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--db=") || strings.HasPrefix(args[i], "--table=") {
			continue
		}
		if !strings.HasPrefix(args[i], "-") && entityArg == "" {
			entityArg = args[i]
		}
	}

	if entityArg == "" {
		return fmt.Errorf("entity path required: domain/entity (e.g. auth/users)\n\nUsage: gopernicus new repo <domain/entity> [--db <name>] [--table <name>]")
	}

	domainName, entityName := parseDomainEntity(entityArg)
	if domainName == "" {
		return fmt.Errorf("domain is required: use domain/entity (e.g. auth/users)\n\nTo bootstrap all repos for a domain: gopernicus boot repos <domain>")
	}

	tableName := tableOverride
	if tableName == "" {
		tableName = generators.Pluralize(entityName)
	}

	root, err := project.MustFindRoot()
	if err != nil {
		return err
	}
	m, err := manifest.Load(root)
	if err != nil {
		return err
	}

	// Resolve framework source for pre-baked bootstrap files.
	fwSourceDir, _ := fwsource.ResolveDir() // empty on error; falls back to generic scaffold

	// Try to find the table in the reflected schema. If found, scaffold
	// full CRUD queries. If not, scaffold a custom repo with a stub.
	table, _, err := findTable(root, m, dbName, tableName, entityName)
	if err != nil {
		// No matching table — scaffold a custom repo.
		return scaffoldCustomRepo(root, domainName, entityName)
	}

	if err := scaffoldRepoForTable(root, dbName, domainName, table, fwSourceDir); err != nil {
		return err
	}

	// Also scaffold bridge.yml for HTTP route configuration.
	if err := scaffoldBridgeYMLForTable(root, domainName, table, fwSourceDir); err != nil {
		return err
	}

	return nil
}

// findTable looks up a table in the reflected schema across all configured schemas.
func findTable(root string, m *manifest.Manifest, dbName, tableName, entityName string) (*schema.TableInfo, string, error) {
	dbConf := m.DatabaseOrDefault(dbName)
	schemaNames := []string{"public"}
	if dbConf != nil {
		schemaNames = dbConf.SchemasOrDefault()
	}

	// Try table name first, then entity name as fallback.
	for _, tryName := range []string{tableName, entityName} {
		for _, s := range schemaNames {
			jsonPath := filepath.Join(root, manifest.MigrationsDir(dbName), "_"+s+".json")
			rs, err := schema.LoadJSON(jsonPath)
			if err != nil {
				continue
			}
			if t, ok := rs.Tables[tryName]; ok {
				return t, s, nil
			}
		}
	}

	return nil, "", fmt.Errorf(
		"table %q not found in reflected schema\n\n"+
			"Run 'gopernicus db reflect' first, or use --table to specify the table name.",
		tableName,
	)
}

// scaffoldRepoForTable creates the repo directory and a queries.sql file
// with default CRUD operations derived from the reflected table schema.
// Go code (model.go, repository.go, store.go) is created by `gopernicus generate`.
func scaffoldRepoForTable(root, dbName, domainName string, table *schema.TableInfo, fwSourceDir string) error {
	tableName := table.TableName
	entitySingular := generators.Singularize(tableName)
	anc := detectAncestry(table)

	repoDir := generators.RepoDir(domainName, tableName, root)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", repoDir, err)
	}

	// If this is a known framework table, use pre-baked files from the
	// framework source (includes custom queries, store methods, repository methods).
	// Otherwise scaffold a generic CRUD queries.sql.
	if repoFiles := fwsource.RepoFiles(fwSourceDir, domainName, tableName); len(repoFiles) > 0 {
		for relPath, content := range repoFiles {
			dest := filepath.Join(repoDir, filepath.FromSlash(relPath))
			if fileExists(dest) {
				fmt.Printf("  skip  %s/%s/%s (already exists)\n", domainName, tableName, relPath)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return fmt.Errorf("creating dir for %s: %w", relPath, err)
			}
			if err := os.WriteFile(dest, content, 0644); err != nil {
				return fmt.Errorf("writing %s: %w", relPath, err)
			}
			fmt.Printf("  create %s/%s/%s\n", domainName, tableName, relPath)
		}
	} else {
		queriesPath := filepath.Join(repoDir, "queries.sql")
		if fileExists(queriesPath) {
			fmt.Printf("  skip  %s/%s/queries.sql (already exists)\n", domainName, tableName)
		} else {
			content := scaffoldQueries(table, dbName, tableName, entitySingular, anc)
			if err := os.WriteFile(queriesPath, []byte(content), 0644); err != nil {
				return fmt.Errorf("writing queries.sql: %w", err)
			}
			fmt.Printf("  create %s/%s/queries.sql\n", domainName, tableName)
		}
	}

	return nil
}

// scaffoldCustomRepo creates a repo directory with a stub queries.sql
// for custom/joined queries that don't map to a single reflected table.
func scaffoldCustomRepo(root, domainName, entityName string) error {
	repoDir := generators.RepoDir(domainName, entityName, root)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", repoDir, err)
	}

	queriesPath := filepath.Join(repoDir, "queries.sql")
	if fileExists(queriesPath) {
		fmt.Printf("  skip %s/%s (queries.sql already exists)\n", domainName, entityName)
		return nil
	}

	content := fmt.Sprintf(`-- Custom queries for %s.
-- No reflected table found — write your queries here.

-- List %s
-- SELECT ... FROM ... ;

`, entityName, generators.ToPascalCase(generators.Pluralize(entityName)))
	if err := os.WriteFile(queriesPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing queries.sql: %w", err)
	}
	fmt.Printf("  create %s/queries.sql (custom)\n", repoDir)
	return nil
}

// scaffoldBridgeYMLForTable creates bridge files for an entity.
// If embedded bridge files exist (framework tables), use those.
// Otherwise scaffold a default bridge.yml from the table schema.
func scaffoldBridgeYMLForTable(root, domainName string, table *schema.TableInfo, fwSourceDir string) error {
	tableName := table.TableName
	entitySingular := generators.Singularize(tableName)
	entityPascal := generators.ToPascalCase(entitySingular)
	anc := detectAncestry(table)

	bridgeDir := generators.BridgeDir(domainName, tableName, root)
	if err := os.MkdirAll(bridgeDir, 0755); err != nil {
		return fmt.Errorf("creating bridge dir: %w", err)
	}

	// Check for bridge files from the framework source.
	repoFiles := fwsource.RepoFiles(fwSourceDir, domainName, tableName)
	for relPath, content := range repoFiles {
		if !strings.HasPrefix(relPath, "bridge/") {
			continue
		}
		// Strip "bridge/" prefix — files go directly into bridgeDir.
		destRel := strings.TrimPrefix(relPath, "bridge/")
		dest := filepath.Join(bridgeDir, destRel)
		if fileExists(dest) {
			fmt.Printf("  skip  bridge/%s/%s (already exists)\n", generators.BridgePackage(tableName), destRel)
			continue
		}
		if err := os.WriteFile(dest, content, 0644); err != nil {
			return fmt.Errorf("writing bridge %s: %w", destRel, err)
		}
		fmt.Printf("  create bridge/%s/%s\n", generators.BridgePackage(tableName), destRel)
	}

	// If bridge.yml already exists (from embedded or previous scaffold), skip.
	ymlPath := filepath.Join(bridgeDir, "bridge.yml")
	if fileExists(ymlPath) {
		fmt.Printf("  skip  bridge.yml (already exists)\n")
		return nil
	}

	content := buildBridgeYMLScaffold(tableName, entityPascal, entitySingular, domainName, table, anc)
	if err := os.WriteFile(ymlPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing bridge.yml: %w", err)
	}
	fmt.Printf("  create %s\n", ymlPath)
	return nil
}

// buildBridgeYMLScaffold generates the default bridge.yml content for an entity.
func buildBridgeYMLScaffold(tableName, entityPascal, entitySingular, domainName string, table *schema.TableInfo, anc ancestry) string {
	pkColumn := ""
	if table.PrimaryKey != nil {
		pkColumn = table.PrimaryKey.Column
	}

	hasSoftDelete := false
	for _, col := range table.Columns {
		if col.Name == "record_state" {
			hasSoftDelete = true
			break
		}
	}

	// Build base path — always verbose with full parent chain.
	// tenant + parent: /tenants/{tenant_id}/questions/{parent_question_id}/takes
	// tenant only:     /tenants/{tenant_id}/questions
	// parent only:     /service-accounts/{service_account_id}/api-keys
	// neither:         /widgets
	resourceSegment := "/" + generators.ToKebabCase(tableName)
	basePath := resourceSegment
	if anc.Parent != nil {
		parentSegment := "/" + generators.ToKebabCase(anc.Parent.RefTable) + "/{" + anc.Parent.Column + "}"
		basePath = parentSegment + resourceSegment
	}
	if anc.Tenant != nil {
		basePath = "/tenants/{" + anc.Tenant.Column + "}" + basePath
	}

	// Resource path (for get/update/delete): basePath + /{pk}
	resourcePath := basePath
	if pkColumn != "" {
		resourcePath = basePath + "/{" + pkColumn + "}"
	}

	// Determine the "nearest parent" for auth — parent if exists, else tenant.
	nearestParent := anc.Parent
	if nearestParent == nil {
		nearestParent = anc.Tenant
	}

	// The authorization param for create — authorize against the nearest parent.
	createAuthzParam := ""
	if nearestParent != nil {
		createAuthzParam = nearestParent.Column
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Bridge configuration for %s.\n", entityPascal)
	fmt.Fprintf(&b, "# Routes and auth schema drive bridge code generation.\n")
	fmt.Fprintf(&b, "# Remove a route to stop generating its handler — write your own in routes.go.\n\n")
	fmt.Fprintf(&b, "entity: %s\n", entityPascal)
	fmt.Fprintf(&b, "repo: %s/%s\n", domainName, generators.ToPackageName(tableName))
	fmt.Fprintf(&b, "domain: %s\n\n", domainName)

	// Auth schema — use nearest parent for ReBAC traversal.
	b.WriteString("auth_relations:\n")
	if nearestParent != nil {
		fmt.Fprintf(&b, "  - \"%s(%s)\"\n", nearestParent.RelName, nearestParent.RelName)
	}
	b.WriteString("  - \"owner(user, service_account)\"\n")
	b.WriteString("\n")

	b.WriteString("auth_permissions:\n")
	if nearestParent != nil {
		fmt.Fprintf(&b, "  - \"list(%s->list)\"\n", nearestParent.RelName)
		fmt.Fprintf(&b, "  - \"create(%s->manage)\"\n", nearestParent.RelName)
		fmt.Fprintf(&b, "  - \"read(owner|%s->read)\"\n", nearestParent.RelName)
		fmt.Fprintf(&b, "  - \"update(owner|%s->manage)\"\n", nearestParent.RelName)
		fmt.Fprintf(&b, "  - \"delete(owner|%s->manage)\"\n", nearestParent.RelName)
		fmt.Fprintf(&b, "  - \"manage(owner|%s->manage)\"\n", nearestParent.RelName)
	} else {
		b.WriteString("  - \"list(owner)\"\n")
		b.WriteString("  - \"create(authenticated)\"\n")
		b.WriteString("  - \"read(owner)\"\n")
		b.WriteString("  - \"update(owner)\"\n")
		b.WriteString("  - \"delete(owner)\"\n")
		b.WriteString("  - \"manage(owner)\"\n")
	}
	b.WriteString("\n")

	b.WriteString("routes:\n")

	// Helper to write a standard middleware chain.
	writeMiddleware := func(authzPerm, authzParam string, isMutation bool) {
		b.WriteString("    middleware:\n")
		if isMutation {
			b.WriteString("      - max_body_size: 1048576\n")
		}
		b.WriteString("      - authenticate: any\n")
		b.WriteString("      - rate_limit\n")
		if authzPerm != "" && authzParam != "" {
			b.WriteString("      - authorize:\n")
			b.WriteString("          permission: " + authzPerm + "\n")
			b.WriteString("          param: " + authzParam + "\n")
		}
	}

	writePrefilterMiddleware := func() {
		b.WriteString("    middleware:\n")
		b.WriteString("      - authenticate: any\n")
		b.WriteString("      - rate_limit\n")
		b.WriteString("      - authorize:\n")
		b.WriteString("          pattern: prefilter\n")
		b.WriteString("          permission: read\n")
		if anc.Tenant != nil {
			fmt.Fprintf(&b, "          subject: \"%s:%s\"\n", anc.Tenant.RelName, anc.Tenant.Column)
		}
	}

	// List — collection path, prefilter
	b.WriteString("  - func: List\n")
	fmt.Fprintf(&b, "    path: %s\n", basePath)
	writePrefilterMiddleware()
	b.WriteString("\n")

	// Get — shallow path
	if pkColumn != "" {
		b.WriteString("  - func: Get\n")
		fmt.Fprintf(&b, "    path: %s\n", resourcePath)
		writeMiddleware("read", pkColumn, false)
		b.WriteString("\n")
	}

	// Create — collection path, params_to_input for parent FKs
	b.WriteString("  - func: Create\n")
	fmt.Fprintf(&b, "    path: %s\n", basePath)
	if anc.Tenant != nil || anc.Parent != nil {
		b.WriteString("    params_to_input:\n")
		if anc.Tenant != nil {
			fmt.Fprintf(&b, "      - %s\n", anc.Tenant.Column)
		}
		if anc.Parent != nil {
			fmt.Fprintf(&b, "      - %s\n", anc.Parent.Column)
		}
	}
	writeMiddleware("create", createAuthzParam, true)
	if pkColumn != "" {
		b.WriteString("    auth_create:\n")
		fmt.Fprintf(&b, "      - \"%s:{%s}#owner@{=subject}\"\n", entitySingular, pkColumn)
		if nearestParent != nil {
			fmt.Fprintf(&b, "      - \"%s:{%s}#%s@%s:{%s}\"\n", entitySingular, pkColumn, nearestParent.RelName, nearestParent.RelName, nearestParent.Column)
		}
	}
	b.WriteString("\n")

	// Update — shallow path
	if pkColumn != "" {
		b.WriteString("  - func: Update\n")
		fmt.Fprintf(&b, "    path: %s\n", resourcePath)
		writeMiddleware("update", pkColumn, true)
		b.WriteString("\n")
	}

	// SoftDelete / Archive / Restore — shallow path
	if pkColumn != "" && hasSoftDelete {
		b.WriteString("  - func: SoftDelete\n")
		b.WriteString("    method: PUT\n")
		fmt.Fprintf(&b, "    path: %s/delete\n", resourcePath)
		writeMiddleware("delete", pkColumn, false)
		b.WriteString("\n")

		b.WriteString("  - func: Archive\n")
		b.WriteString("    method: PUT\n")
		fmt.Fprintf(&b, "    path: %s/archive\n", resourcePath)
		writeMiddleware("update", pkColumn, false)
		b.WriteString("\n")

		b.WriteString("  - func: Restore\n")
		b.WriteString("    method: PUT\n")
		fmt.Fprintf(&b, "    path: %s/restore\n", resourcePath)
		writeMiddleware("update", pkColumn, false)
		b.WriteString("\n")
	}

	// Delete — shallow path
	if pkColumn != "" {
		b.WriteString("  - func: Delete\n")
		fmt.Fprintf(&b, "    path: %s\n", resourcePath)
		writeMiddleware("delete", pkColumn, false)
	}

	return b.String()
}

// ─── parent detection ────────────────────────────────────────────────────────

// parentInfo holds a detected parent FK for an entity.
type parentInfo struct {
	Column   string // FK column name, e.g. "parent_question_id"
	RefTable string // referenced table, e.g. "questions"
	RelName  string // singularized ref table, e.g. "question"
	IsTenant bool   // true if RefTable == "tenants"
}

// ancestry holds the detected tenant and parent relationships for an entity.
type ancestry struct {
	Tenant *parentInfo // tenant FK (tenant_id → tenants), nil if not tenant-scoped
	Parent *parentInfo // direct parent FK (parent_ prefix), nil if none
}

// detectAncestry finds tenant and parent relationships for a table.
//
// Returns both independently:
//   - Tenant: tenant_id FK referencing the tenants table (isolation boundary)
//   - Parent: FK column prefixed with parent_ (direct parent for create/list scoping)
//
// An entity can have both (e.g. takes has tenant_id + parent_question_id),
// just tenant (e.g. questions has tenant_id only), just parent (e.g. api_keys
// has parent_service_account_id), or neither.
func detectAncestry(table *schema.TableInfo) ancestry {
	fkByColumn := make(map[string]schema.ForeignKeyInfo)
	for _, fk := range table.ForeignKeys {
		col := fk.ColumnName
		if len(fk.Columns) > 0 {
			col = fk.Columns[0]
		}
		fkByColumn[col] = fk
	}

	var a ancestry

	// Tenant scoping: tenant_id → tenants.
	if fk, ok := fkByColumn["tenant_id"]; ok && fk.RefTable == "tenants" {
		a.Tenant = &parentInfo{
			Column:   "tenant_id",
			RefTable: "tenants",
			RelName:  "tenant",
			IsTenant: true,
		}
	}

	// Direct parent: FK column with parent_ prefix (excluding tenant).
	for col, fk := range fkByColumn {
		if !strings.HasPrefix(col, "parent_") {
			continue
		}
		if fk.RefTable == "tenants" {
			// parent_tenant_id → tenants is a tenant, not a generic parent.
			if a.Tenant == nil {
				a.Tenant = &parentInfo{
					Column:   col,
					RefTable: "tenants",
					RelName:  "tenant",
					IsTenant: true,
				}
			}
			continue
		}
		a.Parent = &parentInfo{
			Column:   col,
			RefTable: fk.RefTable,
			RelName:  generators.Singularize(fk.RefTable),
		}
	}

	return a
}

// detectSlug returns true if the table has a globally unique slug column —
// i.e. a slug column with a single-column unique constraint. Composite unique
// slugs (e.g. UNIQUE(tenant_id, slug)) are not detected; those require a
// custom GetBySlug query written by the developer.
func detectSlug(table *schema.TableInfo) bool {
	hasSlugCol := false
	for _, col := range table.Columns {
		if col.Name == "slug" && (col.GoType == "string" || col.GoType == "*string") {
			hasSlugCol = true
			if col.IsUnique {
				return true // unique flag set directly on the column
			}
			break
		}
	}
	if !hasSlugCol {
		return false
	}
	// Also check indexes: a unique index where slug is the only column.
	// Skip partial indexes — those encode business rules we can't generalize.
	for _, idx := range table.Indexes {
		if !idx.Unique || idx.Predicate != "" {
			continue
		}
		if len(idx.Columns) == 1 && idx.Columns[0] == "slug" {
			return true
		}
	}
	return false
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// parseDomainEntity splits "auth/users" into ("auth", "users").
// If no slash, domain is empty: "widgets" → ("", "widgets").
func parseDomainEntity(arg string) (domain, entity string) {
	parts := strings.SplitN(arg, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", parts[0]
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// scaffoldQueries generates a default queries.sql with CRUD operations.
//
// Data annotations only — no protocol annotations (@http:json, @authenticated,
// @authorize, @auth.create). Protocol config lives in bridge.yml.
//
// Generated operations (when applicable):
//   List, Get, Create, Update, SoftDelete, Archive, Restore, Delete
func scaffoldQueries(
	table *schema.TableInfo,
	dbName, tableName, entitySingular string,
	anc ancestry,
) string {
	pkColumn := ""
	if table.PrimaryKey != nil {
		pkColumn = table.PrimaryKey.Column
	}

	// Tenant scoping: always add tenant_id to WHERE clauses for list/get.
	tenantCol := ""
	if anc.Tenant != nil {
		tenantCol = anc.Tenant.Column
	}

	// Build WHERE scope parts for all ancestry params.
	// These are appended to every query to enforce tenant + parent isolation.
	var scopeParts []string
	if tenantCol != "" {
		scopeParts = append(scopeParts, fmt.Sprintf("%s = @%s", tenantCol, tenantCol))
	}
	parentCol := ""
	if anc.Parent != nil {
		parentCol = anc.Parent.Column
		scopeParts = append(scopeParts, fmt.Sprintf("%s = @%s", parentCol, parentCol))
	}

	// For list WHERE: "tenant_id = @tenant_id AND parent_question_id = @parent_question_id AND "
	scopeWhere := ""
	if len(scopeParts) > 0 {
		scopeWhere = strings.Join(scopeParts, " AND ") + " AND "
	}

	// For single-record WHERE: " AND tenant_id = @tenant_id AND parent_question_id = @parent_question_id"
	scopeAndClause := ""
	if len(scopeParts) > 0 {
		scopeAndClause = " AND " + strings.Join(scopeParts, " AND ")
	}

	isTenantScoped := tenantCol != ""

	hasSlug := detectSlug(table)

	hasSoftDelete := false
	var searchCols []string
	var filterExclusions []string
	var orderExclusions []string
	var createExclusions []string
	var updateExclusions []string

	for _, col := range table.Columns {
		if col.Name == "record_state" {
			hasSoftDelete = true
		}

		if !col.IsPrimaryKey && !col.IsEnum && !col.IsForeignKey && isStringType(col) {
			if col.Name != "record_state" && !isHashOrSecret(col.Name) {
				searchCols = append(searchCols, col.Name)
			}
		}

		if col.Name == "created_at" || col.Name == "updated_at" {
			createExclusions = append(createExclusions, "-"+col.Name)
		} else if col.IsAutoIncrement {
			createExclusions = append(createExclusions, "-"+col.Name)
		}

		if col.IsPrimaryKey {
			updateExclusions = append(updateExclusions, "-"+col.Name)
		} else if col.Name == "created_at" {
			updateExclusions = append(updateExclusions, "-created_at")
		} else if col.Name == "record_state" {
			updateExclusions = append(updateExclusions, "-record_state")
		} else if isTenantScoped && col.Name == tenantCol {
			updateExclusions = append(updateExclusions, "-"+tenantCol)
		} else if anc.Parent != nil && col.Name == anc.Parent.Column {
			updateExclusions = append(updateExclusions, "-"+anc.Parent.Column)
		}
	}

	filterSpec := buildSpec("*", filterExclusions)
	orderSpec := buildSpec("*", orderExclusions)

	var b strings.Builder
	fmt.Fprintf(&b, "-- @database: %s\n\n", dbName)

	// ─── List ────────────────────────────────────────────────────────────
	b.WriteString("-- @func: List\n")
	fmt.Fprintf(&b, "-- @filter:conditions %s\n", filterSpec)
	if len(searchCols) > 0 {
		fmt.Fprintf(&b, "-- @search: ilike(%s)\n", strings.Join(searchCols, ", "))
	}
	fmt.Fprintf(&b, "-- @order: %s\n", orderSpec)
	b.WriteString("-- @max: 100\n")
	fmt.Fprintf(&b, "SELECT *\nFROM %s\n", tableName)
	if len(searchCols) > 0 {
		fmt.Fprintf(&b, "WHERE %s$conditions AND $search\n", scopeWhere)
	} else {
		fmt.Fprintf(&b, "WHERE %s$conditions\n", scopeWhere)
	}
	b.WriteString("ORDER BY $order\n")
	b.WriteString("LIMIT $limit\n")
	b.WriteString(";\n\n")

	// ─── Get ─────────────────────────────────────────────────────────────
	if pkColumn != "" {
		b.WriteString("-- @func: Get\n")
		fmt.Fprintf(&b, "SELECT *\nFROM %s\nWHERE %s = @%s%s", tableName, pkColumn, pkColumn, scopeAndClause)
		b.WriteString("\n;\n\n")
	}

	// ─── GetBySlug / GetIDBySlug ─────────────────────────────────────────
	if hasSlug && pkColumn != "" {
		softDeleteFilter := "\nAND record_state = 'active'"
		if !hasSoftDelete {
			softDeleteFilter = ""
		}
		b.WriteString("-- @func: GetBySlug\n")
		fmt.Fprintf(&b, "SELECT *\nFROM %s\nWHERE slug = @slug%s%s\n;\n\n", tableName, scopeAndClause, softDeleteFilter)

		b.WriteString("-- @func: GetIDBySlug\n")
		fmt.Fprintf(&b, "-- @returns: %s\n", pkColumn)
		fmt.Fprintf(&b, "SELECT %s\nFROM %s\nWHERE slug = @slug%s%s\n;\n\n", pkColumn, tableName, scopeAndClause, softDeleteFilter)
	}

	// ─── Create ──────────────────────────────────────────────────────────
	createSpec := buildSpec("*", createExclusions)
	b.WriteString("-- @func: Create\n")
	fmt.Fprintf(&b, "-- @fields: %s\n", createSpec)
	fmt.Fprintf(&b, "INSERT INTO %s\n($fields)\nVALUES ($values)\nRETURNING *;\n\n", tableName)

	// ─── Update ──────────────────────────────────────────────────────────
	if pkColumn != "" {
		updateSpec := buildSpec("*", updateExclusions)
		b.WriteString("-- @func: Update\n")
		fmt.Fprintf(&b, "-- @fields: %s\n", updateSpec)
		fmt.Fprintf(&b, "UPDATE %s\nSET $fields\nWHERE %s = @%s%s", tableName, pkColumn, pkColumn, scopeAndClause)
		b.WriteString("\nRETURNING *;\n\n")
	}

	// ─── SoftDelete / Archive / Restore ─────────────────────────────────
	if pkColumn != "" && hasSoftDelete {
		pkAndScope := fmt.Sprintf("%s = @%s%s", pkColumn, pkColumn, scopeAndClause)

		b.WriteString("-- @func: SoftDelete\n")
		fmt.Fprintf(&b, "UPDATE %s\nSET record_state = 'deleted'\nWHERE %s\n;\n\n", tableName, pkAndScope)

		b.WriteString("-- @func: Archive\n")
		fmt.Fprintf(&b, "UPDATE %s\nSET record_state = 'archived'\nWHERE %s\n;\n\n", tableName, pkAndScope)

		b.WriteString("-- @func: Restore\n")
		fmt.Fprintf(&b, "UPDATE %s\nSET record_state = 'active'\nWHERE %s\n;\n\n", tableName, pkAndScope)
	}

	// ─── Delete ──────────────────────────────────────────────────────────
	if pkColumn != "" {
		b.WriteString("-- @func: Delete\n")
		fmt.Fprintf(&b, "DELETE FROM %s\nWHERE %s = @%s%s", tableName, pkColumn, pkColumn, scopeAndClause)
		b.WriteString("\n;\n\n")
	}

	return b.String()
}

// buildSpec builds a column spec string.
// If base is "*" and there are exclusions, returns "*,-col1,-col2".
// If base is "" and exclusions is empty, returns just "*".
func buildSpec(base string, exclusions []string) string {
	if len(exclusions) == 0 {
		return base
	}
	return base + "," + strings.Join(exclusions, ",")
}

// isStringType returns true if the column is a string/text type (for search candidates).
func isStringType(col schema.ColumnInfo) bool {
	goType := strings.TrimPrefix(col.GoType, "*")
	return goType == "string"
}

// isHashOrSecret returns true if the column name suggests it holds a hash or secret.
func isHashOrSecret(name string) bool {
	return strings.Contains(name, "hash") ||
		strings.Contains(name, "secret") ||
		strings.Contains(name, "token") ||
		strings.Contains(name, "password") ||
		strings.Contains(name, "key_prefix")
}
