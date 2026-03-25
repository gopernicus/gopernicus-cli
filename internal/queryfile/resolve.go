package queryfile

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// ResolvedFile is the fully-resolved generation data for one repository,
// produced by cross-referencing a parsed queries.sql with reflected schema.
type ResolvedFile struct {
	Table       *schema.TableInfo
	SchemaName  string // e.g. "public"
	PackageName string // e.g. "usersrepo"
	StorePkg    string // e.g. "userspgxstore"
	EntityName  string // PascalCase singular, e.g. "User"
	EntityLower string // lowercase singular, e.g. "user"
	EntityPlural string // PascalCase plural, e.g. "Users"
	TableName   string // raw table name, e.g. "users"
	DomainName  string // from directory path, e.g. "auth"

	// All columns from the reflected schema — used for entity struct generation.
	AllColumns []schema.ColumnInfo

	// Primary key info.
	PKColumn string // e.g. "user_id"
	PKGoName string // e.g. "UserID"
	PKGoType string // e.g. "string"

	Queries []ResolvedQuery
}

// FieldInfo holds resolved type information for a single field.
type FieldInfo struct {
	GoName   string // PascalCase, e.g. "Email"
	GoType   string // e.g. "string", "*time.Time"
	GoImport string // e.g. "time"
	DBName   string // e.g. "email"
	IsTime   bool
	IsEnum   bool
}

// OrderByField holds an orderable column.
type OrderByField struct {
	ConstName string // e.g. "OrderByCreatedAt"
	DBColumn  string // e.g. "created_at"
	GoName    string // e.g. "CreatedAt"
}

// ResolvedQuery is a query block with fully resolved type information.
type ResolvedQuery struct {
	QueryBlock // embedded parsed query

	FuncName     string            // Go function name, e.g. "ListUsers"
	ParamTypes   map[string]string // param name → Go type (from schema lookup)
	FilterFields []FieldInfo       // filter struct fields (from $filters inline spec)
	OrderFields  []OrderByField    // orderable columns (from $order inline spec)
	SetFields    []FieldInfo       // updatable fields (from @fields on UPDATE)
	InsertFields []FieldInfo       // insertable fields (from @fields on INSERT)
	SearchFields []FieldInfo       // ILIKE search fields (from @search annotation)
	ReturnFields []FieldInfo       // custom return columns (from @returns, explicit SELECT, or explicit RETURNING)
	MaxLimit     int               // from $lim inline spec
	SearchType   string            // "ilike", "web_search", "tsvector" — from @search annotation
}

// Resolve cross-references parsed queries with the reflected schema to produce
// fully-typed generation data. If qf.Table is empty (custom repo), returns an
// error — custom repos need a different resolution path (future work).
func Resolve(qf *File, s *schema.ReflectedSchema, domainName string) (*ResolvedFile, error) {
	if qf.Table == "" {
		return nil, fmt.Errorf("table not set on parsed file (set File.Table before calling Resolve)")
	}

	table, ok := s.Tables[qf.Table]
	if !ok {
		return nil, fmt.Errorf("table %q not found in reflected schema", qf.Table)
	}

	entityRaw := generators.Singularize(qf.Table)
	entityPascal := generators.ToPascalCase(entityRaw)

	pkColumn := ""
	pkGoName := ""
	pkGoType := "string"
	if table.PrimaryKey != nil {
		pkColumn = table.PrimaryKey.Column
		pkGoType = strings.TrimPrefix(table.PrimaryKey.GoType, "*")
		pkGoName = generators.ToPascalCase(pkColumn)
	}

	rf := &ResolvedFile{
		Table:        table,
		SchemaName:   s.SchemaName,
		PackageName:  generators.RepoPackage(qf.Table),
		StorePkg:     generators.StorePackage(qf.Table, "pgx"), // TODO: resolve from manifest
		EntityName:   entityPascal,
		EntityLower:  strings.ToLower(entityRaw),
		EntityPlural: generators.ToPascalCase(generators.Pluralize(entityRaw)),
		TableName:    qf.Table,
		DomainName:   domainName,
		AllColumns:   table.Columns,
		PKColumn:     pkColumn,
		PKGoName:     pkGoName,
		PKGoType:     pkGoType,
	}

	// Build a column lookup map.
	colMap := buildColumnMap(table)

	for _, qb := range qf.Queries {
		rq, err := resolveQuery(qb, table, colMap)
		if err != nil {
			return nil, fmt.Errorf("query %q: %w", qb.Name, err)
		}
		rf.Queries = append(rf.Queries, rq)
	}

	return rf, nil
}

func resolveQuery(qb QueryBlock, table *schema.TableInfo, colMap map[string]schema.ColumnInfo) (ResolvedQuery, error) {
	rq := ResolvedQuery{
		QueryBlock: qb,
		FuncName:   qb.Name,
	}

	// Resolve $lim inline spec → MaxLimit.
	if qb.LimitSpec != "" {
		n, err := strconv.Atoi(qb.LimitSpec)
		if err != nil {
			return rq, fmt.Errorf("$lim spec %q: %w", qb.LimitSpec, err)
		}
		rq.MaxLimit = n
	}

	// Resolve $filters inline spec → FilterFields.
	if qb.FilterSpec != "" {
		fields, err := resolveFieldSpec(qb.FilterSpec, table, colMap)
		if err != nil {
			return rq, fmt.Errorf("$filters spec: %w", err)
		}
		rq.FilterFields = fields
	}

	// Resolve $order inline spec → OrderFields.
	if qb.OrderSpec != "" {
		orderFields, err := resolveOrderSpec(qb.OrderSpec, table, colMap)
		if err != nil {
			return rq, fmt.Errorf("$order spec: %w", err)
		}
		rq.OrderFields = orderFields
	}

	// Resolve @search annotation → SearchFields + SearchType.
	if searchStr, ok := qb.Annotations["search"]; ok {
		fields, err := resolveSearchSpec(searchStr, table, colMap)
		if err != nil {
			return rq, fmt.Errorf("@search: %w", err)
		}
		rq.SearchFields = fields
		rq.SearchType = qb.SearchType // populated by parser
	} else if qb.HasFilters {
		// Default: all string-typed, non-enum columns with ilike search.
		rq.SearchFields = defaultSearchFields(table)
		rq.SearchType = "ilike"
	}

	// Resolve return fields — priority: @returns annotation > explicit SELECT cols > explicit RETURNING cols.
	// When any of these is set, the query returns a custom result type instead of the full entity.
	switch {
	case qb.Annotations["returns"] != "":
		fields, err := resolveFieldSpec(qb.Annotations["returns"], table, colMap)
		if err != nil {
			return rq, fmt.Errorf("@returns: %w", err)
		}
		rq.ReturnFields = fields
	case qb.SelectCols != "":
		fields, err := resolveFieldSpec(qb.SelectCols, table, colMap)
		if err != nil {
			return rq, fmt.Errorf("explicit SELECT cols: %w", err)
		}
		rq.ReturnFields = fields
	case qb.ReturnCols != "":
		fields, err := resolveFieldSpec(qb.ReturnCols, table, colMap)
		if err != nil {
			return rq, fmt.Errorf("explicit RETURNING cols: %w", err)
		}
		rq.ReturnFields = fields
	}

	// Resolve @fields annotation → InsertFields or SetFields.
	if fieldsStr, ok := qb.Annotations["fields"]; ok {
		fields, err := resolveFieldSpec(fieldsStr, table, colMap)
		if err != nil {
			return rq, fmt.Errorf("@fields: %w", err)
		}
		switch qb.Type {
		case QueryInsert:
			rq.InsertFields = fields
		case QueryUpdate:
			rq.SetFields = fields
		}
	}

	// Resolve named parameter types.
	if len(qb.Params) > 0 {
		rq.ParamTypes = make(map[string]string, len(qb.Params))
		for _, p := range qb.Params {
			col, ok := colMap[p]
			if !ok {
				return rq, fmt.Errorf("param @%s: column %q not found in table %q", p, p, table.TableName)
			}
			// Use non-pointer type for params (they're always required).
			rq.ParamTypes[p] = strings.TrimPrefix(col.GoType, "*")
		}
	}

	return rq, nil
}

// resolveSearchSpec parses "@search: ilike(field1, field2)" and returns FieldInfo entries.
// Supports future search types like "tsvector(title, body)" with the same pattern.
func resolveSearchSpec(spec string, table *schema.TableInfo, colMap map[string]schema.ColumnInfo) ([]FieldInfo, error) {
	spec = strings.TrimSpace(spec)

	// Parse "type(field1, field2)" format.
	if parenIdx := strings.IndexByte(spec, '('); parenIdx > 0 && strings.HasSuffix(spec, ")") {
		inner := spec[parenIdx+1 : len(spec)-1]
		return resolveFieldSpec(inner, table, colMap)
	}

	// Fallback: treat as plain comma-separated field list.
	return resolveFieldSpec(spec, table, colMap)
}

// ─── field/order spec resolution ─────────────────────────────────────────────

// resolveFieldSpec parses a field spec like "*,-password_hash,-created_at"
// and returns the resolved FieldInfo list.
//
// Syntax:
//   - "*" = all columns from the table
//   - "-col" = exclude column
//   - "col" = include column (explicit inclusion)
//   - Mixed: "*,-excluded1,-excluded2" = all except excluded
func resolveFieldSpec(spec string, table *schema.TableInfo, colMap map[string]schema.ColumnInfo) ([]FieldInfo, error) {
	parts := strings.Split(spec, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	includeAll := false
	excluded := make(map[string]bool)
	var explicit []string

	for _, p := range parts {
		if p == "*" {
			includeAll = true
		} else if strings.HasPrefix(p, "-") {
			excluded[p[1:]] = true
		} else if p != "" {
			explicit = append(explicit, p)
		}
	}

	var result []FieldInfo

	if includeAll {
		for _, col := range table.Columns {
			if excluded[col.Name] {
				continue
			}
			result = append(result, colToFieldInfo(col))
		}
	} else {
		for _, name := range explicit {
			col, ok := colMap[name]
			if !ok {
				return nil, fmt.Errorf("column %q not found in table %q", name, table.TableName)
			}
			result = append(result, colToFieldInfo(col))
		}
	}

	return result, nil
}

// resolveOrderSpec parses an order spec and returns OrderByField entries.
// Supports the same syntax as resolveFieldSpec:
//   - "*" = all columns
//   - "*,-col" = all except excluded
//   - "col1,col2" = explicit columns
func resolveOrderSpec(spec string, table *schema.TableInfo, colMap map[string]schema.ColumnInfo) ([]OrderByField, error) {
	parts := strings.Split(spec, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	includeAll := false
	excluded := make(map[string]bool)
	var explicit []string

	for _, p := range parts {
		if p == "*" {
			includeAll = true
		} else if strings.HasPrefix(p, "-") {
			excluded[p[1:]] = true
		} else if p != "" {
			explicit = append(explicit, p)
		}
	}

	var result []OrderByField

	if includeAll {
		for _, col := range table.Columns {
			if excluded[col.Name] {
				continue
			}
			result = append(result, OrderByField{
				ConstName: "OrderBy" + generators.ToPascalCase(col.Name),
				DBColumn:  col.Name,
				GoName:    generators.ToPascalCase(col.Name),
			})
		}
	} else {
		for _, name := range explicit {
			if _, ok := colMap[name]; !ok {
				return nil, fmt.Errorf("column %q not found in table %q", name, table.TableName)
			}
			result = append(result, OrderByField{
				ConstName: "OrderBy" + generators.ToPascalCase(name),
				DBColumn:  name,
				GoName:    generators.ToPascalCase(name),
			})
		}
	}

	return result, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func buildColumnMap(table *schema.TableInfo) map[string]schema.ColumnInfo {
	m := make(map[string]schema.ColumnInfo, len(table.Columns))
	for _, col := range table.Columns {
		m[col.Name] = col
	}
	return m
}

// defaultSearchFields returns all string-typed, non-enum columns as search fields.
func defaultSearchFields(table *schema.TableInfo) []FieldInfo {
	var result []FieldInfo
	for _, col := range table.Columns {
		if col.IsEnum {
			continue
		}
		goType := strings.TrimPrefix(col.GoType, "*")
		if goType == "string" {
			result = append(result, colToFieldInfo(col))
		}
	}
	return result
}

func colToFieldInfo(col schema.ColumnInfo) FieldInfo {
	return FieldInfo{
		GoName:   generators.ToPascalCase(col.Name),
		GoType:   col.GoType,
		GoImport: col.GoImport,
		DBName:   col.Name,
		IsTime:   col.GoImport == "time",
		IsEnum:   col.IsEnum,
	}
}
