// Package generators is a fresh code generation pipeline that reads
// annotated queries.sql files and generates repository + pgxstore layers.
package generators

import (
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// ─── parsed types (from queries.sql) ─────────────────────────────────────────

// File represents a parsed queries.sql file.
type File struct {
	// Database is the database config name (from @database, default: "primary").
	Database string

	// Table is the primary table for this repo. Not parsed from the file —
	// set externally by the caller (inferred from directory name or manifest).
	Table string

	// FileAnnotations holds file-level annotations beyond @database.
	FileAnnotations map[string]string

	Queries []QueryBlock
}

// QueryBlock represents a single named query with its annotations and SQL.
type QueryBlock struct {
	Name string // PascalCase function name from @func

	// Annotations are @key: value pairs on this query (excluding @func and @filter).
	Annotations map[string]string

	// SQL is the raw SQL text (between @func and terminating semicolon).
	SQL string

	Type   QueryType
	Params []string // Named params found in SQL (@user_id → "user_id")

	// Named filters: placeholder name → field spec.
	// e.g. {"conditions": "*,-record_state", "status": "record_state"}
	// Populated from @filter:name annotations.
	Filters map[string]string

	HasFilters  bool // derived: len(Filters) > 0
	HasSearch   bool // SQL contains $search
	HasFields   bool // SQL contains $fields
	HasValues   bool // SQL contains $values
	HasOrder    bool // SQL contains $order
	HasLimit    bool // SQL contains $limit
	ReturnsRows bool // SELECT or has RETURNING clause

	// Annotation-based specs (all above-query).
	OrderSpec  string // from @order: annotation
	LimitSpec  string // from @max: annotation
	SearchSpec string // from @search: annotation (e.g. "ilike(email, display_name)")

	// Cache spec from @cache: annotation (e.g. "5m").
	CacheSpec string

	// Event type from @event: annotation (e.g. "user.created").
	EventType string

	// TypeHints maps param name → Go type, from @type:param_name go_type annotations.
	// Overrides column-inferred types in resolve.
	TypeHints map[string]string

	// ScanOverride explicitly sets the store category, bypassing inference.
	// Set by @scan: <value> annotation. Supported values: "many", "one", "exec".
	// Useful when the generator can't infer the correct scan mode (e.g. UPDATE...RETURNING
	// with FOR UPDATE SKIP LOCKED should return []Entity, not a single Entity).
	ScanOverride string

	// Explicit column lists — kept for fallback when AST parsing fails.
	SelectCols string
	ReturnCols string
}

// QueryType classifies a query by its SQL verb.
type QueryType int

const (
	QuerySelect QueryType = iota
	QueryInsert
	QueryUpdate
	QueryDelete
)

// String returns the SQL verb for this query type.
func (qt QueryType) String() string {
	switch qt {
	case QuerySelect:
		return "SELECT"
	case QueryInsert:
		return "INSERT"
	case QueryUpdate:
		return "UPDATE"
	case QueryDelete:
		return "DELETE"
	default:
		return "UNKNOWN"
	}
}

// ─── resolved types (after schema cross-reference) ───────────────────────────

// ResolvedFile is the fully-resolved generation data for one repository.
type ResolvedFile struct {
	Table        *schema.TableInfo
	SchemaName   string // e.g. "public"
	PackageName  string // e.g. "users"
	StorePkg     string // e.g. "userspgx"
	EntityName   string // PascalCase singular, e.g. "User"
	EntityLower  string // lowercase singular, e.g. "user"
	EntityPlural string // PascalCase plural, e.g. "Users"
	TableName    string // raw table name, e.g. "users"
	DomainName   string // from directory path, e.g. "auth"

	AllColumns []schema.ColumnInfo

	PKColumn string // e.g. "user_id"
	PKGoName string // e.g. "UserID"
	PKGoType string // e.g. "string"

	// Auth schema for this entity (from @auth.relations / @auth.permissions).
	AuthRelations   []AuthRelation
	AuthPermissions []AuthPermission

	Queries []ResolvedQuery
}

// FieldInfo holds resolved type information for a single field.
type FieldInfo struct {
	GoName        string // PascalCase, e.g. "Email"
	GoType        string // e.g. "string", "*time.Time"
	GoImport      string // e.g. "time"
	DBName        string // unqualified column name, e.g. "email"
	QualifiedName string // table-qualified column for SQL, e.g. "u.email" (empty = use DBName)
	IsTime        bool
	IsEnum        bool

	// Schema constraints (from reflected schema, used by bridge validation).
	IsNullable   bool
	IsPrimaryKey bool
	IsForeignKey bool
	HasDefault   bool
	MaxLength    int      // varchar(N) character max length
	EnumValues   []string // allowed values for enum types
	DBType       string   // raw DB type, e.g. "uuid", "varchar(255)"
}

// SQLName returns the qualified name if set, otherwise DBName.
func (f FieldInfo) SQLName() string {
	if f.QualifiedName != "" {
		return f.QualifiedName
	}
	return f.DBName
}

// OrderByField holds an orderable column.
type OrderByField struct {
	ConstName string // e.g. "OrderByCreatedAt"
	DBColumn  string // e.g. "created_at"
	GoName    string // e.g. "CreatedAt"
}

// ResolvedFilter holds resolved type information for one named filter.
type ResolvedFilter struct {
	Name   string      // placeholder name ("conditions", "status")
	Spec   string      // raw spec ("*,-record_state")
	Fields []FieldInfo // resolved fields from spec
}

// AuthRelation represents a parsed relation from @auth.relations.
type AuthRelation struct {
	Name     string   // e.g. "owner", "member"
	Subjects []string // e.g. ["user", "service_account"]
}

// AuthPermission represents a parsed permission from @auth.permissions.
type AuthPermission struct {
	Name  string   // e.g. "read", "update"
	Rules []string // e.g. ["owner", "manager", "tenant->member"]
}

// AuthorizeSpec represents a parsed @authorize annotation.
// Drives bridge generation for prefilter/postfilter/check patterns.
type AuthorizeSpec struct {
	// Pattern is "prefilter", "postfilter", or "check".
	Pattern string

	// Permission is the action to check (e.g. "read", "update", "delete").
	Permission string

	// Param is the path parameter name to authorize against for check patterns.
	// e.g. "tenant_id" or "question_id". Required for check on nested routes
	// where the first path param may not be the resource being authorized.
	Param string

	// SubjectRef is the optional explicit subject reference for prefilter.
	// Empty means "use authenticated subject from context".
	// e.g. "tenant:tenant_id" means subject is tenant:{tenant_id} from URL param.
	SubjectRef string
}

// AuthCreateRel represents a parsed relationship creation from @auth.create.
type AuthCreateRel struct {
	ResourceType string // e.g. "tenant"
	ResourceID   string // e.g. "{tenant_id}"
	Relation     string // e.g. "owner"
	SubjectType  string // e.g. "{subject}" or "service_account"
	SubjectID    string // e.g. "" (implied) or "{service_account_id}"
}

// ResolvedQuery is a query block with fully resolved type information.
type ResolvedQuery struct {
	QueryBlock // embedded parsed query

	FuncName        string            // Go function name
	ParamTypes      map[string]string // param name → Go type
	ResolvedFilters []ResolvedFilter  // named filters with resolved fields
	OrderFields     []OrderByField    // orderable columns
	SetFields       []FieldInfo       // updatable fields (from @fields on UPDATE)
	InsertFields    []FieldInfo       // insertable fields (from @fields on INSERT)
	SearchFields    []FieldInfo       // search fields (from @search)
	ReturnFields    []FieldInfo       // custom return columns
	MaxLimit        int               // from @limit: annotation
	SearchType      string            // "ilike", "web_search", "tsvector"
	CacheTTL        string            // Go duration expression, e.g. "5 * time.Minute"
}

// AllFilterFields returns all filter fields across all named filters, deduplicated.
// Used by downstream consumers that need a flat field list (e.g., filter struct generation).
func (rq ResolvedQuery) AllFilterFields() []FieldInfo {
	seen := make(map[string]bool)
	var result []FieldInfo
	for _, rf := range rq.ResolvedFilters {
		for _, f := range rf.Fields {
			if !seen[f.DBName] {
				seen[f.DBName] = true
				result = append(result, f)
			}
		}
	}
	return result
}
