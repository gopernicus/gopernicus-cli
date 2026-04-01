package generators

// goperniculusFrameworkPath is the canonical module path for the gopernicus framework.
// Framework packages (sdk, bridge/transit, core/auth, infrastructure) always use this
// path regardless of the project module path.
const goperniculusFrameworkPath = "github.com/gopernicus/gopernicus"

// BridgeTemplateData holds all data needed for bridge template rendering.
type BridgeTemplateData struct {
	// Package info
	BridgePackage string // e.g. "tenantsrepobridge"
	RepoPackage   string // e.g. "tenantsrepo"
	ModulePath    string // project module path from go.mod (for local imports only)
	FrameworkPath string // gopernicus framework module path (for sdk, bridge, auth, infra imports)
	Module        string // domain name, e.g. "auth"

	// Entity naming
	EntityName       string // "Tenant"
	EntityNameLower  string // "tenant"
	EntityNamePlural string // "Tenants"

	// Primary key
	PKColumn   string // "tenant_id"
	PKGoName   string // "TenantID"
	PKGoType   string // "string"
	PKURLParam string // "tenant_id"

	// Per-query data for list/create/update handlers.
	// Each list query gets its own query params struct, filter parser, etc.
	ListQueries   []BridgeListQuery
	CreateQueries []BridgeCreateQuery
	UpdateQueries []BridgeUpdateQuery

	// Routes (built from @http:json annotations)
	Routes []BridgeRoute

	// Feature flags (from gopernicus.yml features section)
	AuthEnabled bool // gopernicus auth — injects authenticator/authorizer into bridge

	// Auth relationship creation — true when any Create route has @auth.create.
	HasCreateRels bool

	// All create rels across all Create queries (for createAuthRelationships method).
	AllCreateRels []BridgeCreateRel

	// Auth relationship cleanup — true when any Delete/HardDelete route exists
	// and auth is enabled. Injects deleteAuthRelationships into generated bridge.
	HasDeleteRels bool

	// Entity singular name for delete cleanup (e.g. "user").
	EntitySingular string

	// PostfilterRoutes holds deduplicated postfilter list routes.
	// Used to determine whether to import bridge/fop and to generate call sites.
	PostfilterRoutes    []BridgeRoute
	HasPostfilterRoutes bool

	// Import flags
	NeedsTimeImport          bool
	NeedsStrconvImport       bool
	NeedsFmtImport           bool
	NeedsJSONImport          bool
	NeedsValidationImport    bool
	NeedsStringsImport       bool
	NeedsContextImport       bool
	NeedsAuthorizationImport bool
	NeedsHTTPMidImport       bool
	NeedsBridgeFOPImport     bool
}

// BridgeListQuery holds per-list-query data for filter/order/pagination generation.
// Each @func with filters/order/limit gets its own query params struct and parsers.
type BridgeListQuery struct {
	FuncName     string        // "List", "ListByTenant"
	FilterFields []BridgeField // fields for QueryParams{FuncName} and parseFilter{FuncName}
	HasSearch    bool          // whether this list has $search
}

// BridgeCreateQuery holds per-create-query data for request model generation.
type BridgeCreateQuery struct {
	FuncName string        // "Create"
	Fields   []BridgeField // fields for GeneratedCreate{EntityName}Request
}

// BridgeUpdateQuery holds per-update-query data for request model generation.
type BridgeUpdateQuery struct {
	FuncName string        // "Update"
	Fields   []BridgeField // fields for GeneratedUpdate{EntityName}Request
}

// BridgeField describes a field used in bridge request/response models.
type BridgeField struct {
	GoName       string // "Email"
	GoType       string // "string", "*string"
	UpdateGoType string // For update requests: always pointer. "*string" or "*string" (same for nullable)
	DBName       string // "email"
	IsTime       bool
	IsString     bool
	IsPointer    bool

	// Schema constraints (for validation generation).
	IsRequired bool     // NOT NULL && no default && not PK/FK
	MaxLength  int      // varchar(N) character max length; 0 = no limit
	IsEnum     bool     // column is enum type
	EnumValues []string // allowed enum values

	// Name/type-based heuristic validators.
	IsEmail bool // column name matches email pattern
	IsURL   bool // column name matches url pattern
	IsSlug  bool // column name matches slug pattern
	IsUUID  bool // DB type is uuid
}

// BridgeRoute represents a single HTTP route generated from an @http:json annotation.
type BridgeRoute struct {
	// From @http:json annotation
	Method string // "GET"
	Path   string // "/tenants/:tenant_id"

	// From the query function this route maps to
	FuncName    string // "Get", "List", "Create"
	HandlerName string // "httpGet", "httpList", "httpCreate"

	// Derived from query category
	Category string // "list", "scan_one", "create", "update", "exec"

	// For list handlers
	HasFilters     bool
	HasOrder       bool
	MaxLimit       int
	FilterTypeName string // from repo generated code, e.g. "FilterList"

	// For handlers that read path params
	PathParams []PathParam

	// For create/update handlers
	HasBody bool

	// MaxBodySize overrides the default body size limit (bytes). 0 = use default.
	MaxBodySize int64

	// ParamsToInput lists path params to inject into the repo input struct.
	// Each entry is a PathParam that should be set on the input after ToRepo().
	ParamsToInput []PathParam

	// MiddlewareChain is the ordered middleware list from bridge.yml.
	// Used by addGeneratedRoutes to render the middleware chain.
	MiddlewareChain []MiddlewareEntry

	// For handlers with explicit params beyond path params
	ExplicitParams []ExplicitParam

	// Auth relationship creation from @auth.create annotation.
	CreateRels []BridgeCreateRel

	// DeleteCleanup indicates this exec route should clean up auth relationships
	// before performing the delete. Set for hard-delete routes when auth is enabled.
	DeleteCleanup bool

	// DeleteCleanupGoName is the Go variable name of the path param that
	// identifies the resource being deleted (e.g. "tenantID"). Resolved by
	// matching the entity's PK column against the route's path params.
	DeleteCleanupGoName string

	// Authorization spec from @authorize annotation.
	// Drives prefilter/postfilter/check patterns in generated handlers.
	Authorize *AuthorizeSpec

	// Authenticated specifies the authentication mode for this route.
	// Values: "user", "service_account", "user_session", "any", or "" (none).
	Authenticated string

	// WithPermissions indicates the handler should inline an authorization check and
	// include the caller's relation and permissions alongside the record in the response.
	// Set by the @with:permissions annotation on scan_one funcs with @authorize: check(...).
	WithPermissions bool

	// SlugPKParam is set for GetBySlug routes. It holds the PK URL param name
	// that httpmid.SlugToID will inject via SetPathValue (e.g. "tenant_id").
	// Empty for all other routes.
	SlugPKParam string

	// SlugPKGoField is the Go struct field name on GetIDBySlugResult used to
	// extract the resolved ID (e.g. "TenantID"). Set alongside SlugPKParam.
	SlugPKGoField string

	// EntityRepoField is the bridge struct field that holds the repository,
	// used to build the SlugToID resolver closure (e.g. "tenantRepository").
	// Set alongside SlugPKParam for GetBySlug routes.
	EntityRepoField string
}

// BridgeCreateRel holds a resolved auth relationship for template rendering.
// Built from AuthCreateRel by resolving {placeholders} to Go expressions.
type BridgeCreateRel struct {
	ResourceType       string // literal: "user"
	ResourceIDExpr     string // Go expression: "record.UserID"
	Relation           string // literal: "self"
	SubjectFromContext bool   // true → use subject type/ID from authenticated context
	SubjectType        string // literal type when not from context: "service_account"
	SubjectIDExpr      string // Go expression when not from context: "record.ServiceAccountID"
}

// PathParam represents a path parameter extracted from the route path.
type PathParam struct {
	Name        string // "tenant_id" (from {tenant_id} in path)
	GoName      string // "tenantID" (camelCase for local var)
	GoFieldName string // "TenantID" (PascalCase for struct field)
	GoType      string // "string"
}

// ExplicitParam represents an explicit parameter that the repo function expects
// beyond what's captured from the path.
type ExplicitParam struct {
	Name   string // "tenant_id"
	GoName string // "TenantID"
	GoType string // "string"
}
