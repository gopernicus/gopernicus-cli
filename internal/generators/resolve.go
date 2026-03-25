package generators

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// reColOpParam matches "column_name op @param" (e.g. "expires_at > @now").
var reColOpParam = regexp.MustCompile(`(?i)\b(\w+)\s*(?:<=|>=|<>|!=|<|>|=)\s*@(\w+)`)

// reParamOpCol matches "@param op column_name" (e.g. "@now < expires_at").
var reParamOpCol = regexp.MustCompile(`(?i)@(\w+)\s*(?:<=|>=|<>|!=|<|>|=)\s*(\w+)\b`)

// inferParamTypeFromSQL scans raw SQL for comparisons like "expires_at > @now"
// and returns a param→GoType map derived from the compared column's schema type.
func inferParamTypeFromSQL(sql string, allColMap map[string]schema.ColumnInfo) map[string]string {
	result := make(map[string]string)
	for _, m := range reColOpParam.FindAllStringSubmatch(sql, -1) {
		colName, paramName := strings.ToLower(m[1]), strings.ToLower(m[2])
		if col, ok := allColMap[colName]; ok {
			result[paramName] = strings.TrimPrefix(col.GoType, "*")
		}
	}
	for _, m := range reParamOpCol.FindAllStringSubmatch(sql, -1) {
		paramName, colName := strings.ToLower(m[1]), strings.ToLower(m[2])
		if _, already := result[paramName]; !already {
			if col, ok := allColMap[colName]; ok {
				result[paramName] = strings.TrimPrefix(col.GoType, "*")
			}
		}
	}
	return result
}

// Resolve cross-references parsed queries with the reflected schema to produce
// fully-typed generation data.
func Resolve(qf *File, s *schema.ReflectedSchema, domainName string) (*ResolvedFile, error) {
	if qf.Table == "" {
		return nil, fmt.Errorf("table not set on parsed file (set File.Table before calling Resolve)")
	}

	table, ok := s.Tables[qf.Table]
	if !ok {
		return nil, fmt.Errorf("table %q not found in reflected schema", qf.Table)
	}

	entityRaw := Singularize(qf.Table)
	entityPascal := ToPascalCase(entityRaw)

	pkColumn := ""
	pkGoName := ""
	pkGoType := "string"
	if table.PrimaryKey != nil {
		pkColumn = table.PrimaryKey.Column
		pkGoType = strings.TrimPrefix(table.PrimaryKey.GoType, "*")
		pkGoName = ToPascalCase(pkColumn)
	}

	rf := &ResolvedFile{
		Table:        table,
		SchemaName:   s.SchemaName,
		PackageName:  RepoPackage(qf.Table),
		StorePkg:     StorePackage(qf.Table, "pgx"),
		EntityName:   entityPascal,
		EntityLower:  strings.ToLower(entityRaw),
		EntityPlural: ToPascalCase(Pluralize(entityRaw)),
		TableName:    qf.Table,
		DomainName:   domainName,
		AllColumns:   table.Columns,
		PKColumn:     pkColumn,
		PKGoName:     pkGoName,
		PKGoType:     pkGoType,
	}

	colMap := buildColumnMap(table)

	// Build a cross-table column lookup for resolving JOIN/CTE columns.
	allColMap := buildAllColumnMap(s.Tables)

	for _, qb := range qf.Queries {
		rq, err := resolveQuery(qb, table, colMap, allColMap)
		if err != nil {
			return nil, fmt.Errorf("query %q: %w", qb.Name, err)
		}
		rf.Queries = append(rf.Queries, rq)
	}

	return rf, nil
}

func resolveQuery(qb QueryBlock, table *schema.TableInfo, colMap, allColMap map[string]schema.ColumnInfo) (ResolvedQuery, error) {
	rq := ResolvedQuery{
		QueryBlock: qb,
		FuncName:   qb.Name,
	}

	// Resolve @max annotation → MaxLimit.
	if qb.LimitSpec != "" {
		n, err := strconv.Atoi(qb.LimitSpec)
		if err != nil {
			return rq, fmt.Errorf("@max spec %q: %w", qb.LimitSpec, err)
		}
		rq.MaxLimit = n
	}

	// Resolve @cache annotation → CacheTTL (Go duration expression).
	if qb.CacheSpec != "" {
		ttl, err := parseTTLToDuration(qb.CacheSpec)
		if err != nil {
			return rq, fmt.Errorf("@cache spec %q: %w", qb.CacheSpec, err)
		}
		rq.CacheTTL = ttl
	}

	// Resolve named filters → ResolvedFilters.
	if len(qb.Filters) > 0 {
		for name, spec := range qb.Filters {
			fields, err := resolveFieldSpec(spec, table, colMap)
			if err != nil {
				return rq, fmt.Errorf("@filter:%s spec: %w", name, err)
			}
			rq.ResolvedFilters = append(rq.ResolvedFilters, ResolvedFilter{
				Name:   name,
				Spec:   spec,
				Fields: fields,
			})
		}
	}

	// Resolve @order: annotation → OrderFields.
	if qb.OrderSpec != "" {
		orderFields, err := resolveOrderSpec(qb.OrderSpec, table, colMap)
		if err != nil {
			return rq, fmt.Errorf("$order spec: %w", err)
		}
		rq.OrderFields = orderFields
	}

	// Resolve @search: annotation → SearchFields + SearchType.
	if qb.SearchSpec != "" {
		st, _ := parseSearchAnnotation(qb.SearchSpec)
		fields, err := resolveSearchSpec(qb.SearchSpec, table, colMap)
		if err != nil {
			return rq, fmt.Errorf("@search: %w", err)
		}
		rq.SearchFields = fields
		rq.SearchType = st
	} else if qb.HasFilters {
		rq.SearchFields = defaultSearchFields(table)
		rq.SearchType = "ilike"
	}

	// Resolve return fields.
	// Priority: @returns annotation > AST-based resolution > fallback to string-based.
	if qb.Annotations["returns"] != "" {
		fields, err := resolveFieldSpec(qb.Annotations["returns"], table, colMap)
		if err != nil {
			return rq, fmt.Errorf("@returns: %w", err)
		}
		rq.ReturnFields = fields
	} else {
		fields, err := resolveReturnFieldsAST(qb, table, colMap, allColMap)
		if err != nil {
			return rq, fmt.Errorf("AST resolution: %w", err)
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
	// Priority: @type hint > primary table column > any table column > SQL comparison context > name heuristic.
	if len(qb.Params) > 0 {
		rq.ParamTypes = make(map[string]string, len(qb.Params))
		sqlContext := inferParamTypeFromSQL(qb.SQL, allColMap)
		for _, p := range qb.Params {
			// @type hints take highest priority.
			if hint, ok := qb.TypeHints[p]; ok {
				rq.ParamTypes[p] = hint
				continue
			}
			col, ok := colMap[p]
			if !ok {
				col, ok = allColMap[p]
			}
			if ok {
				rq.ParamTypes[p] = strings.TrimPrefix(col.GoType, "*")
			} else if t, ok := sqlContext[p]; ok {
				rq.ParamTypes[p] = t
			} else {
				rq.ParamTypes[p] = inferParamType(p)
			}
		}
	}

	return rq, nil
}

// ─── AST-based return field resolution ───────────────────────────────────────

// resolveReturnFieldsAST uses pg_query AST to extract and type-resolve output columns.
// Returns nil, nil for SELECT * / RETURNING * (caller uses all schema columns).
// Returns nil, error if AST parsing fails (caller should fall back to string-based).
func resolveReturnFieldsAST(qb QueryBlock, table *schema.TableInfo, colMap, allColMap map[string]schema.ColumnInfo) ([]FieldInfo, error) {
	// Collect filter placeholder names for PrepareForParse.
	var filterNames []string
	for name := range qb.Filters {
		filterNames = append(filterNames, name)
	}

	prepared := PrepareForParse(qb.SQL, filterNames)
	ast, err := ParseSQL(prepared)
	if err != nil {
		return nil, err
	}

	// Determine which column list to resolve.
	var cols []ASTColumn
	switch {
	case len(ast.ReturnCols) > 0:
		cols = ast.ReturnCols
	case len(ast.SelectCols) > 0:
		cols = ast.SelectCols
	default:
		return nil, nil
	}

	// Check for star — means all columns from schema, no custom return type needed.
	if len(cols) == 1 && cols[0].NodeType == "star" {
		return nil, nil
	}

	// Build CTE column type map for cross-reference.
	cteTypeMap := make(map[string]map[string]ASTColumn)
	for cteName, cteCols := range ast.CTEColumns {
		m := make(map[string]ASTColumn, len(cteCols))
		for _, c := range cteCols {
			m[c.Name] = c
		}
		cteTypeMap[cteName] = m
	}

	var fields []FieldInfo
	for _, col := range cols {
		if col.NodeType == "star" {
			// t.* — include all columns from that table (skip for now, rare).
			continue
		}
		fi := resolveASTColumn(col, ast.AliasMap, cteTypeMap, colMap, allColMap)
		fields = append(fields, fi)
	}

	return fields, nil
}

// resolveASTColumn resolves a single AST column to a FieldInfo using this priority:
//  1. AST-inferred type (func, sublink, const, coalesce with fallback)
//  2. CTE column map (via alias resolution)
//  3. Schema column map (primary table, then all tables)
//  4. Name-based heuristic fallback
func resolveASTColumn(col ASTColumn, aliasMap map[string]string, cteTypeMap map[string]map[string]ASTColumn, colMap, allColMap map[string]schema.ColumnInfo) FieldInfo {
	// 1. If AST already inferred a type, use it.
	if col.InferredType != "" && col.InferredType != "any" {
		goImport := ""
		if strings.Contains(col.InferredType, "time.") || col.InferredType == "time.Time" {
			goImport = "time"
		}
		if strings.Contains(col.InferredType, "json.") {
			goImport = "encoding/json"
		}
		return FieldInfo{
			GoName:   ToPascalCase(col.Name),
			GoType:   col.InferredType,
			GoImport: goImport,
			DBName:   col.Name,
			IsTime:   goImport == "time",
		}
	}

	// 2. Try CTE column map via alias resolution.
	if col.SourceTable != "" {
		realName := col.SourceTable
		if mapped, ok := aliasMap[col.SourceTable]; ok {
			realName = mapped
		}
		if cteCols, ok := cteTypeMap[realName]; ok {
			sourceCol := col.SourceCol
			if sourceCol == "" {
				sourceCol = col.Name
			}
			if cteCol, ok := cteCols[sourceCol]; ok {
				if cteCol.InferredType != "" && cteCol.InferredType != "any" {
					goImport := ""
					if strings.Contains(cteCol.InferredType, "time.") || cteCol.InferredType == "time.Time" {
						goImport = "time"
					}
					if strings.Contains(cteCol.InferredType, "json.") {
						goImport = "encoding/json"
					}
					return FieldInfo{
						GoName:   ToPascalCase(col.Name),
						GoType:   cteCol.InferredType,
						GoImport: goImport,
						DBName:   col.Name,
						IsTime:   goImport == "time",
					}
				}
			}
		}
	}

	// 3. Try schema column map.
	lookupCol := col.SourceCol
	if lookupCol == "" {
		lookupCol = col.Name
	}
	if schemaCol, ok := colMap[lookupCol]; ok {
		return FieldInfo{
			GoName:   ToPascalCase(col.Name),
			GoType:   schemaCol.GoType,
			GoImport: schemaCol.GoImport,
			DBName:   col.Name,
			IsTime:   schemaCol.GoImport == "time",
			IsEnum:   schemaCol.IsEnum,
		}
	}
	if schemaCol, ok := allColMap[lookupCol]; ok {
		return FieldInfo{
			GoName:   ToPascalCase(col.Name),
			GoType:   schemaCol.GoType,
			GoImport: schemaCol.GoImport,
			DBName:   col.Name,
			IsTime:   schemaCol.GoImport == "time",
			IsEnum:   schemaCol.IsEnum,
		}
	}

	// 4. Name-based heuristic fallback.
	goType := inferColumnTypeByName(col.Name)
	return FieldInfo{
		GoName: ToPascalCase(col.Name),
		GoType: goType,
		DBName: col.Name,
		IsTime: goType == "*time.Time" || goType == "time.Time",
	}
}

// resolveSearchSpec parses "@search: ilike(field1, field2)" and returns FieldInfo entries.
func resolveSearchSpec(spec string, table *schema.TableInfo, colMap map[string]schema.ColumnInfo) ([]FieldInfo, error) {
	spec = strings.TrimSpace(spec)

	if parenIdx := strings.IndexByte(spec, '('); parenIdx > 0 && strings.HasSuffix(spec, ")") {
		inner := spec[parenIdx+1 : len(spec)-1]
		return resolveFieldSpec(inner, table, colMap)
	}

	return resolveFieldSpec(spec, table, colMap)
}

// resolveFieldSpec parses a field spec like "*,-password_hash,-created_at"
// and returns the resolved FieldInfo list.
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
			// Strip table alias prefix (e.g. "u.email" → lookup "email", keep "u.email" as qualified).
			qualified := ""
			lookupName := name
			if dot := strings.IndexByte(name, '.'); dot >= 0 {
				qualified = name
				lookupName = name[dot+1:]
			}
			col, ok := colMap[lookupName]
			if !ok {
				return nil, fmt.Errorf("column %q not found in table %q", lookupName, table.TableName)
			}
			fi := colToFieldInfo(col)
			fi.QualifiedName = qualified
			result = append(result, fi)
		}
	}

	return result, nil
}

// resolveOrderSpec parses an order spec and returns OrderByField entries.
// Explicit column names are accepted even if they don't exist in the primary
// table — they may come from CTEs, aggregates, or joined columns.
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
				ConstName: "OrderBy" + ToPascalCase(col.Name),
				DBColumn:  col.Name,
				GoName:    ToPascalCase(col.Name),
			})
		}
	} else {
		for _, name := range explicit {
			// Strip table alias prefix for Go names (e.g. "a.last_active_at" → GoName "LastActiveAt").
			// Preserve full name for DBColumn so generated SQL uses the qualified reference.
			colName := name
			if dot := strings.IndexByte(name, '.'); dot >= 0 {
				colName = name[dot+1:]
			}
			result = append(result, OrderByField{
				ConstName: "OrderBy" + ToPascalCase(colName),
				DBColumn:  name,
				GoName:    ToPascalCase(colName),
			})
		}
	}

	return result, nil
}

// inferColumnTypeByName guesses the Go type from column naming conventions.
// Only used as a fallback for columns not found in any schema column map
// (CTE-derived columns, expression aliases, etc.).
func inferColumnTypeByName(name string) string {
	lower := strings.ToLower(name)
	switch {
	// Boolean patterns.
	case strings.HasPrefix(lower, "has_"),
		strings.HasPrefix(lower, "is_"),
		strings.HasPrefix(lower, "can_"),
		strings.HasPrefix(lower, "was_"):
		return "bool"

	// Time patterns.
	case strings.HasSuffix(lower, "_at"),
		strings.HasSuffix(lower, "_date"),
		strings.HasPrefix(lower, "last_"),
		strings.Contains(lower, "timestamp"):
		return "*time.Time"

	// Count/numeric patterns.
	case strings.HasSuffix(lower, "_count"),
		strings.HasPrefix(lower, "count_"),
		strings.HasPrefix(lower, "total_"),
		strings.HasPrefix(lower, "num_"):
		return "int64"

	default:
		return "any"
	}
}

// inferParamType guesses the Go type for a named param that doesn't match any
// column. Infers from the parameter name; defaults to string.
func inferParamType(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, "_at") || strings.HasSuffix(lower, "_since") ||
		strings.HasSuffix(lower, "_before") || strings.HasSuffix(lower, "_after") ||
		strings.HasSuffix(lower, "_date"):
		return "time.Time"
	case strings.HasSuffix(lower, "_count") || strings.HasSuffix(lower, "_limit") ||
		strings.HasSuffix(lower, "_offset"):
		return "int"
	case strings.HasSuffix(lower, "_flag") || strings.HasPrefix(lower, "is_") ||
		strings.HasPrefix(lower, "has_"):
		return "bool"
	default:
		return "string"
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func buildColumnMap(table *schema.TableInfo) map[string]schema.ColumnInfo {
	m := make(map[string]schema.ColumnInfo, len(table.Columns))
	for _, col := range table.Columns {
		m[col.Name] = col
	}
	return m
}

// buildAllColumnMap builds a flat column lookup across all tables in the schema.
// If multiple tables share a column name, the first one wins — the primary
// table's colMap is always checked first during resolution.
func buildAllColumnMap(tables map[string]*schema.TableInfo) map[string]schema.ColumnInfo {
	m := make(map[string]schema.ColumnInfo)
	for _, t := range tables {
		for _, col := range t.Columns {
			if _, exists := m[col.Name]; !exists {
				m[col.Name] = col
			}
		}
	}
	return m
}

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

// parseTTLToDuration converts a TTL string like "5m", "1h", "30s" to a Go
// duration expression like "5 * time.Minute".
func parseTTLToDuration(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", fmt.Errorf("empty TTL spec")
	}

	unit := spec[len(spec)-1:]
	num := strings.TrimSpace(spec[:len(spec)-1])

	if _, err := strconv.Atoi(num); err != nil {
		return "", fmt.Errorf("invalid TTL number %q", num)
	}

	switch unit {
	case "s":
		return num + " * time.Second", nil
	case "m":
		return num + " * time.Minute", nil
	case "h":
		return num + " * time.Hour", nil
	default:
		return "", fmt.Errorf("unsupported TTL unit %q (use s, m, or h)", unit)
	}
}

func colToFieldInfo(col schema.ColumnInfo) FieldInfo {
	return FieldInfo{
		GoName:       ToPascalCase(col.Name),
		GoType:       col.GoType,
		GoImport:     col.GoImport,
		DBName:       col.Name,
		IsTime:       col.GoImport == "time",
		IsEnum:       col.IsEnum,
		IsNullable:   col.IsNullable,
		IsPrimaryKey: col.IsPrimaryKey,
		IsForeignKey: col.IsForeignKey,
		HasDefault:   col.HasDefault,
		MaxLength:    col.MaxLength,
		EnumValues:   col.EnumValues,
		DBType:       col.DBType,
	}
}
