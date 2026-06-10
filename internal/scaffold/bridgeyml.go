package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gopernicus/gopernicus-cli/internal/fwsource"
	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// bridgeYMLScaffoldTemplate renders the default bridge.yml for an entity.
// The "middleware" sub-template writes a standard chain (mw builds its args);
// "prefilter" writes the list route's prefilter authorization chain.
const bridgeYMLScaffoldTemplate = `{{define "middleware"}}    middleware:
{{if .Mutation}}      - max_body_size: 1048576
{{end}}      - authenticate: any
      - rate_limit
{{if and .Perm .Param}}      - authorize:
          permission: {{.Perm}}
          param: {{.Param}}
{{end}}{{end}}{{define "prefilter"}}    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          pattern: prefilter
          permission: read
{{if .HasTenant}}          subject: "{{.TenantRel}}:{{.TenantColumn}}"
{{end}}{{end}}# Bridge configuration for {{.EntityPascal}}.
# Routes and auth schema drive bridge code generation.
# Remove a route to stop generating its handler — write your own in routes.go.

entity: {{.EntityPascal}}
repo: {{.Repo}}
domain: {{.Domain}}

auth_relations:
{{if .NearestRel}}  - "{{.NearestRel}}({{.NearestRel}})"
{{end}}  - "owner(user, service_account)"

auth_permissions:
{{if .NearestRel}}  - "list({{.NearestRel}}->list)"
  - "create({{.NearestRel}}->manage)"
  - "read(owner|{{.NearestRel}}->read)"
  - "update(owner|{{.NearestRel}}->manage)"
  - "delete(owner|{{.NearestRel}}->manage)"
  - "manage(owner|{{.NearestRel}}->manage)"
{{else}}  - "list(owner)"
  - "create(authenticated)"
  - "read(owner)"
  - "update(owner)"
  - "delete(owner)"
  - "manage(owner)"
{{end}}
routes:
  - func: List
    path: {{.BasePath}}
{{template "prefilter" .}}
{{if .PKColumn}}  - func: Get
    path: {{.ResourcePath}}
{{template "middleware" (mw "read" .PKColumn false)}}
{{end}}  - func: Create
    path: {{.BasePath}}
{{if or .HasTenant .HasParent}}    params_to_input:
{{if .HasTenant}}      - {{.TenantColumn}}
{{end}}{{if .HasParent}}      - {{.ParentColumn}}
{{end}}{{end}}{{template "middleware" (mw "create" .CreateAuthzParam true)}}{{if .PKColumn}}    auth_create:
      - "{{.EntitySingular}}:{{.PKBrace}}#owner@{=subject}"
{{if .NearestRel}}      - "{{.EntitySingular}}:{{.PKBrace}}#{{.NearestRel}}@{{.NearestRel}}:{{.NearestColBrace}}"
{{end}}{{end}}
{{if .PKColumn}}  - func: Update
    path: {{.ResourcePath}}
{{template "middleware" (mw "update" .PKColumn true)}}
{{end}}{{if and .PKColumn .HasSoftDelete}}  - func: SoftDelete
    method: PUT
    path: {{.ResourcePath}}/delete
{{template "middleware" (mw "delete" .PKColumn false)}}
  - func: Archive
    method: PUT
    path: {{.ResourcePath}}/archive
{{template "middleware" (mw "update" .PKColumn false)}}
  - func: Restore
    method: PUT
    path: {{.ResourcePath}}/restore
{{template "middleware" (mw "update" .PKColumn false)}}
{{end}}{{if .PKColumn}}  - func: Delete
    path: {{.ResourcePath}}
{{template "middleware" (mw "delete" .PKColumn false)}}{{end}}`

// bridgeYMLScaffoldTmpl is parsed once; mw bundles the middleware
// sub-template's arguments (authorize permission/param, mutation flag).
var bridgeYMLScaffoldTmpl = template.Must(
	template.New("bridge_yml_scaffold").Funcs(template.FuncMap{
		"mw": func(perm, param string, mutation bool) map[string]any {
			return map[string]any{"Perm": perm, "Param": param, "Mutation": mutation}
		},
	}).Parse(bridgeYMLScaffoldTemplate),
)

// bridgeYMLScaffoldData feeds bridgeYMLScaffoldTemplate.
type bridgeYMLScaffoldData struct {
	EntityPascal   string
	EntitySingular string
	Repo           string // repo key, e.g. "auth/users"
	Domain         string

	BasePath     string // collection path with full parent chain
	ResourcePath string // basePath + /{pk}

	PKColumn      string // "" when the table has no primary key
	PKBrace       string // "{<pk>}", for auth_create tuples
	HasSoftDelete bool

	HasTenant    bool
	TenantColumn string
	TenantRel    string

	HasParent    bool
	ParentColumn string

	// Nearest parent for auth — parent if it exists, else tenant.
	NearestRel       string // "" when the entity has neither
	NearestColBrace  string // "{<nearest parent column>}"
	CreateAuthzParam string // authorize param for create, "" when no parent
}

// BridgeYMLForTable creates bridge files for an entity.
// If embedded bridge files exist (framework tables), use those.
// Otherwise scaffold a default bridge.yml from the table schema.
func BridgeYMLForTable(root, domainName string, table *schema.TableInfo, fwSourceDir string) error {
	tableName := table.TableName
	entitySingular := generators.Singularize(tableName)
	entityPascal := generators.ToPascalCase(entitySingular)
	anc := DetectAncestry(table)

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
func buildBridgeYMLScaffold(tableName, entityPascal, entitySingular, domainName string, table *schema.TableInfo, anc Ancestry) string {
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

	data := bridgeYMLScaffoldData{
		EntityPascal:     entityPascal,
		EntitySingular:   entitySingular,
		Repo:             domainName + "/" + generators.ToPackageName(tableName),
		Domain:           domainName,
		BasePath:         basePath,
		ResourcePath:     resourcePath,
		PKColumn:         pkColumn,
		HasSoftDelete:    hasSoftDelete,
		CreateAuthzParam: createAuthzParam,
	}
	if pkColumn != "" {
		data.PKBrace = "{" + pkColumn + "}"
	}
	if anc.Tenant != nil {
		data.HasTenant = true
		data.TenantColumn = anc.Tenant.Column
		data.TenantRel = anc.Tenant.RelName
	}
	if anc.Parent != nil {
		data.HasParent = true
		data.ParentColumn = anc.Parent.Column
	}
	if nearestParent != nil {
		data.NearestRel = nearestParent.RelName
		data.NearestColBrace = "{" + nearestParent.Column + "}"
	}

	var b strings.Builder
	if err := bridgeYMLScaffoldTmpl.Execute(&b, data); err != nil {
		// Static template over plain string/bool data — cannot fail at runtime.
		panic(fmt.Sprintf("rendering bridge.yml scaffold: %v", err))
	}
	return b.String()
}
