// Package queryfile parses annotated SQL query files (queries.sql) used to
// drive code generation for repository and store layers.
//
// File format:
//
//	-- @database: primary
//
//	-- @func: ListUsers
//	-- @search: ilike(email, display_name)
//	SELECT *
//	FROM users
//	WHERE $filters -- *,-password_hash
//	ORDER BY $order -- *,-password_hash
//	LIMIT $lim -- 200
//	;
//
//	-- @func: GetUser
//	SELECT * FROM users WHERE user_id = @user_id;
package queryfile

// File represents a parsed queries.sql file.
type File struct {
	// Database is the database config name (from @database, default: "primary").
	Database string

	// Table is the primary table for this repo. Not parsed from the file —
	// set externally by the caller (inferred from directory name or manifest).
	// Empty for custom/join repos that don't map to a single table.
	Table string

	// FileAnnotations holds file-level annotations beyond @database.
	// Future use: @parent, @relations, @permissions.
	FileAnnotations map[string]string

	Queries []QueryBlock
}

// QueryBlock represents a single named query with its annotations and SQL.
type QueryBlock struct {
	Name string // PascalCase function name from @func, e.g. "ListUsers"

	// Annotations are @key: value pairs on this query (excluding @func).
	// Known keys: "fields", "search". Future: "auth", "event", "cache", "on_create".
	Annotations map[string]string

	// SQL is the raw SQL text (between the @func comment and the terminating
	// semicolon). Inline spec comments (-- spec) are stripped.
	SQL string

	Type        QueryType
	Params      []string // Named params found in SQL (@user_id → "user_id")
	HasFilters  bool     // SQL contains $filters placeholder
	HasFields   bool     // SQL contains $fields placeholder
	HasValues   bool     // SQL contains $values placeholder
	HasOrder    bool     // SQL contains $order placeholder
	HasLimit    bool     // SQL contains $lim placeholder
	ReturnsRows bool     // SELECT or has RETURNING clause

	// Inline specs extracted from SQL comments.
	// e.g. "$filters -- *,-record_state" → FilterSpec = "*,-record_state"
	FilterSpec string // column spec for $filters
	OrderSpec  string // column spec for $order
	LimitSpec  string // default limit for $lim

	// SelectCols holds the explicit column list when the SELECT clause does
	// NOT use "*". Empty means SELECT * (expand from schema at generation time).
	// e.g. "SELECT user_id, email FROM users" → SelectCols = "user_id, email"
	SelectCols string

	// ReturnCols holds the explicit column list when a RETURNING clause does
	// NOT use "*". Empty means RETURNING * (expand from schema at generation time).
	// For SELECT queries, RETURNING acts as a directive specifying result columns.
	// e.g. "RETURNING user_id, email" → ReturnCols = "user_id, email"
	ReturnCols string

	// SearchType is the search function from @search annotation.
	// Parsed from "ilike(cols)", "web_search(cols)", "tsvector(cols)".
	// Empty when no @search annotation is present.
	SearchType string
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
