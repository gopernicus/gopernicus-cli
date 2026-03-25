package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// ─── pgxstore template data types ────────────────────────────────────────────

// StoreFieldInfo holds template data for a single field in pgxstore context.
type StoreFieldInfo struct {
	GoName        string
	DBName        string // unqualified, e.g. "email"
	QualifiedName string // table-qualified, e.g. "u.email" (empty = use DBName)
	IsTime        bool
}

// SQLName returns the qualified name if set, otherwise DBName.
func (f StoreFieldInfo) SQLName() string {
	if f.QualifiedName != "" {
		return f.QualifiedName
	}
	return f.DBName
}

// NamedArg holds a named argument mapping for template rendering.
type NamedArg struct {
	ArgName string // key in pgx.NamedArgs
	GoExpr  string // Go expression for the value
}

// SearchFieldInfo holds a searchable text field.
type SearchFieldInfo struct {
	DBName        string // unqualified, e.g. "email"
	QualifiedName string // table-qualified, e.g. "u.email" (empty = use DBName)
}

// SQLName returns the qualified name if set, otherwise DBName.
func (f SearchFieldInfo) SQLName() string {
	if f.QualifiedName != "" {
		return f.QualifiedName
	}
	return f.DBName
}

// StoreFilter holds data for one named filter in a list method.
type StoreFilter struct {
	PlaceholderName string // "conditions"
	GenFuncName     string // "generatedApplyListUsersConditionsFilter"
	FilterTypeName  string // "FilterListUsers" (copied from parent for template access)
	Fields             []StoreFieldInfo // resolved fields for this filter
	HasRecordState     bool             // this filter includes record_state
	RecordStateSQLName string           // qualified name for record_state in SQL (e.g. "u.record_state")
}

// StoreMethod holds data for one pgxstore method implementation.
type StoreMethod struct {
	FuncName string
	Params   string
	Returns  string
	Category string

	// For scan_one / scan_one_custom.
	SQL            string
	NamedArgs      []NamedArg
	ReturnTypeName string

	// For create.
	InsertCols      string
	InsertVals      string
	CreateArgs      []NamedArg
	HasAppCreatedAt bool // entity has created_at but it's excluded from @fields
	HasAppUpdatedAt bool // entity has updated_at but it's excluded from @fields

	// For update / update_returning.
	UpdateFields        []StoreFieldInfo
	WhereField          string
	WhereGoVar          string
	HasUpdatedAt        bool
	UpdateReturningCols string

	// For exec.
	ExecSQL      string
	ExecArgs     []NamedArg
	IsSoftDelete bool
	SkipRowCheck bool // from @check_rows: false — skip RowsAffected == 0 check

	// For list — named filters with placeholder substitution.
	FilterTypeName      string
	Filters             []StoreFilter    // one per named filter
	SearchFields        []SearchFieldInfo
	SearchType          string
	HasSearch           bool
	GenSearchFunc string // generated: "generatedApplyListUsersSearchFilter"
	RecordStateInFilter bool   // any filter includes record_state

	// List sub-features (for conditional template rendering).
	HasFilters     bool
	HasOrder       bool
	HasLimit       bool
	HasBaseWhere   bool       // not used with placeholder substitution, kept for reference.
	BaseSQL        string     // Pre-built SQL with $order/$limit stripped, filter placeholders preserved.
	BaseArgs       []NamedArg // Named args for base SQL params.
	ListReturnType string     // Custom return type for list (empty = entity).
}

// StoreTemplateData holds all data needed to render pgxstore templates.
type StoreTemplateData struct {
	PackageName     string
	RepoPkg         string
	RepoImport      string
	QualifiedTable  string
	EntityName      string
	EntityNameLower string

	PKColumn string
	PKGoName string

	NeedsTime     bool
	HasSoftDelete bool

	EntityColList string
	AllCols       string

	HasCreate bool
	HasUpdate bool
	HasFilter bool
	HasList   bool

	Methods []StoreMethod
}

// GeneratePgxStore produces pgxstore files for the given entity.
func GeneratePgxStore(
	resolved *ResolvedFile,
	domainName string,
	modulePath string,
	projectRoot string,
	opts Options,
) error {
	data, err := buildStoreData(resolved, domainName, modulePath)
	if err != nil {
		return fmt.Errorf("build pgxstore data for %s: %w", resolved.TableName, err)
	}

	outDir := StoreDir(domainName, resolved.TableName, "pgx", projectRoot)
	if err := ensureDir(outDir, opts); err != nil {
		return fmt.Errorf("create dir %s: %w", outDir, err)
	}

	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	genFiles := []genFile{
		{"generated.go", storeGeneratedTemplate, false},
		{"store.go", storeBootstrapTemplate, true},
	}

	for _, f := range genFiles {
		path := filepath.Join(outDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderStoreTemplate(f.tmpl, data)
		if err != nil {
			return fmt.Errorf("render %s for %s: %w", f.name, resolved.TableName, err)
		}

		formatted, err := format.Source(out)
		if err != nil {
			_ = writeFile(path, out, opts)
			return fmt.Errorf("go/format %s: %w\nUnformatted output written for debugging.", f.name, err)
		}

		if err := writeFile(path, formatted, opts); err != nil {
			return fmt.Errorf("write %s: %w", f.name, err)
		}

		verb := "write"
		if f.bootstrap {
			verb = "create"
		}
		fmt.Printf("      %s %s\n", verb, path)
	}

	return nil
}

func renderStoreTemplate(tmplText string, data StoreTemplateData) ([]byte, error) {
	t, err := template.New("").Parse(tmplText)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}


func buildStoreData(resolved *ResolvedFile, domainName, modulePath string) (StoreTemplateData, error) {
	repoPkg := RepoPackage(resolved.TableName)
	repoImport := modulePath + "/core/repositories/"
	if domainName != "" {
		repoImport += domainName + "/"
	}
	repoImport += ToPackageName(resolved.TableName)

	qualifiedTable := resolved.SchemaName + "." + resolved.TableName

	hasSoftDelete := false
	hasUpdatedAt := false
	var allColNames []string

	for _, col := range resolved.AllColumns {
		if col.Name == "record_state" {
			hasSoftDelete = true
		}
		if col.Name == "updated_at" {
			hasUpdatedAt = true
		}
		allColNames = append(allColNames, col.Name)
	}

	allCols := strings.Join(allColNames, ", ")
	entityColList := formatColList(allColNames)

	hasCreate, hasUpdate, hasFilter, hasList := false, false, false, false

	for _, rq := range resolved.Queries {
		if len(rq.InsertFields) > 0 {
			hasCreate = true
		}
		if len(rq.SetFields) > 0 {
			hasUpdate = true
		}
		if rq.HasFilters || rq.HasOrder || rq.HasLimit {
			hasList = true
		}
		if rq.HasFilters {
			hasFilter = true
		}
	}

	var methods []StoreMethod
	for _, rq := range resolved.Queries {
		m := buildStoreMethod(rq, resolved, qualifiedTable, allCols, hasUpdatedAt, hasSoftDelete)
		methods = append(methods, m)
	}

	// NeedsTime when any method uses time.Now().UTC().
	needsTime := hasUpdatedAt && hasUpdate
	for _, m := range methods {
		if m.HasAppCreatedAt || m.HasAppUpdatedAt {
			needsTime = true
			break
		}
	}

	return StoreTemplateData{
		PackageName:     StorePackage(resolved.TableName, "pgx"),
		RepoPkg:         repoPkg,
		RepoImport:      repoImport,
		QualifiedTable:  qualifiedTable,
		EntityName:      resolved.EntityName,
		EntityNameLower: resolved.EntityLower,
		PKColumn:        resolved.PKColumn,
		PKGoName:        resolved.PKGoName,
		NeedsTime:       needsTime,
		HasSoftDelete:   hasSoftDelete,
		EntityColList:   entityColList,
		AllCols:         allCols,
		HasCreate:       hasCreate,
		HasUpdate:       hasUpdate,
		HasFilter:       hasFilter,
		HasList:         hasList,
		Methods:         methods,
	}, nil
}

// ─── category resolution ─────────────────────────────────────────────────────

func resolveCategory(rq ResolvedQuery) string {
	// @scan: <value> overrides inferred category.
	switch rq.ScanOverride {
	case "many":
		return "scan_many"
	case "one":
		return "scan_one"
	case "exec":
		return "exec"
	}
	switch {
	case rq.HasFilters || rq.HasOrder || rq.HasLimit:
		return "list"
	case rq.HasFields && rq.Type == QueryInsert:
		return "create"
	case rq.HasFields && rq.Type == QueryUpdate && rq.ReturnsRows:
		return "update_returning"
	case rq.HasFields && rq.Type == QueryUpdate:
		return "update"
	case rq.ReturnsRows && len(rq.ReturnFields) > 0:
		return "scan_one_custom"
	case rq.ReturnsRows:
		return "scan_one"
	default:
		return "exec"
	}
}

// ─── parameter + return type building ────────────────────────────────────────

func buildStoreParamList(rq ResolvedQuery, repoPkg, entityName, category string) string {
	params := []string{"ctx context.Context"}

	switch category {
	case "list":
		if rq.HasFilters {
			filterTypeName := "Filter" + rq.FuncName
			params = append(params, "filter "+repoPkg+"."+filterTypeName)
		}
		// Add explicit params (e.g., @tenant_id) before orderBy/page.
		params = append(params, appendStoreParamArgs(rq)...)
		if rq.HasOrder {
			params = append(params,
				"orderBy fop.Order",
				"page fop.PageStringCursor",
				"forPrevious bool",
			)
		} else if rq.HasLimit {
			params = append(params, "limit int")
		}
	case "create":
		params = append(params, "input "+repoPkg+".Create"+entityName)
	case "update", "update_returning":
		params = append(params, appendStoreParamArgs(rq)...)
		params = append(params, "input "+repoPkg+".Update"+entityName)
	case "scan_one", "scan_one_custom", "scan_many", "exec":
		params = append(params, appendStoreParamArgs(rq)...)
	}

	return strings.Join(params, ", ")
}

func appendStoreParamArgs(rq ResolvedQuery) []string {
	var params []string
	for _, p := range rq.Params {
		goType := "string"
		if t, ok := rq.ParamTypes[p]; ok {
			goType = t
		}
		params = append(params, ToCamelCase(p)+" "+goType)
	}
	return params
}

func buildStoreReturnType(rq ResolvedQuery, repoPkg, entityName, category string) string {
	switch category {
	case "list", "scan_many":
		// Default to entity type; buildListStoreMethod overrides if custom return type needed.
		return "([]" + repoPkg + "." + entityName + ", error)"
	case "create", "scan_one":
		return "(" + repoPkg + "." + entityName + ", error)"
	case "scan_one_custom":
		return "(" + repoPkg + "." + rq.FuncName + "Result, error)"
	case "update_returning":
		if len(rq.ReturnFields) > 0 {
			return "(" + repoPkg + "." + rq.FuncName + "Result, error)"
		}
		return "(" + repoPkg + "." + entityName + ", error)"
	case "update", "exec":
		return "error"
	default:
		return "error"
	}
}

// ─── per-category method builders ────────────────────────────────────────────

func buildListStoreMethod(m *StoreMethod, rq ResolvedQuery, resolved *ResolvedFile, allCols string) {
	m.HasFilters = rq.HasFilters
	m.HasOrder = rq.HasOrder
	m.HasLimit = rq.HasLimit

	// Always compute BaseSQL — for filter AND non-filter lists.
	// BaseSQL preserves named filter placeholders ($conditions, $status, $search);
	// only $order and $limit lines are stripped.
	m.BaseSQL = buildListBaseSQL(rq.QueryBlock, resolved.SchemaName, resolved.TableName)
	m.HasBaseWhere = strings.Contains(strings.ToUpper(m.BaseSQL), " WHERE ")
	m.BaseArgs = buildNamedArgList(rq.Params)

	if rq.HasFilters {
		m.FilterTypeName = "Filter" + rq.FuncName

		// Build one StoreFilter per named filter.
		for _, rf := range rq.ResolvedFilters {
			sf := StoreFilter{
				PlaceholderName: rf.Name,
				GenFuncName:     "generatedApply" + rq.FuncName + ToPascalCase(rf.Name) + "Filter",
				FilterTypeName:  "Filter" + rq.FuncName,
			}
			for _, f := range rf.Fields {
				sf.Fields = append(sf.Fields, StoreFieldInfo{
					GoName:        f.GoName,
					DBName:        f.DBName,
					QualifiedName: f.QualifiedName,
					IsTime:        f.IsTime,
				})
				if f.DBName == "record_state" {
					sf.HasRecordState = true
					sf.RecordStateSQLName = f.SQLName()
					m.RecordStateInFilter = true
				}
			}
			m.Filters = append(m.Filters, sf)
		}

		for _, f := range rq.SearchFields {
			m.SearchFields = append(m.SearchFields, SearchFieldInfo{DBName: f.DBName, QualifiedName: f.QualifiedName})
		}
		m.SearchType = rq.SearchType
		m.HasSearch = rq.HasSearch && len(rq.SearchFields) > 0
		if m.HasSearch {
			m.GenSearchFunc = "generatedApply" + rq.FuncName + "SearchFilter"
		}
	}

	// Custom return type for lists with explicit SELECT containing non-entity columns.
	if len(rq.ReturnFields) > 0 && hasNonEntityColumns(rq.ReturnFields, resolved.AllColumns) {
		repoPkg := RepoPackage(resolved.TableName)
		m.ListReturnType = repoPkg + "." + rq.FuncName + "Result"
		m.Returns = "([]" + m.ListReturnType + ", error)"
	}
}

// hasNonEntityColumns returns true if any return field is not an entity column.
func hasNonEntityColumns(returnFields []FieldInfo, entityCols []schema.ColumnInfo) bool {
	entitySet := make(map[string]bool, len(entityCols))
	for _, col := range entityCols {
		entitySet[col.Name] = true
	}
	for _, f := range returnFields {
		if !entitySet[f.DBName] {
			return true
		}
	}
	return false
}

func buildCreateStoreMethod(m *StoreMethod, rq ResolvedQuery, allColumns []schema.ColumnInfo) {
	var cols, vals []string
	var createArgs []NamedArg
	for _, f := range rq.InsertFields {
		cols = append(cols, f.DBName)
		vals = append(vals, "@"+f.DBName)
		createArgs = append(createArgs, NamedArg{
			ArgName: f.DBName,
			GoExpr:  "input." + f.GoName,
		})
	}
	m.InsertCols = strings.Join(cols, ", ")
	m.InsertVals = strings.Join(vals, ", ")
	m.CreateArgs = createArgs

	// Detect timestamp columns excluded from @fields — these get app-side UTC values.
	insertSet := make(map[string]bool, len(rq.InsertFields))
	for _, f := range rq.InsertFields {
		insertSet[f.DBName] = true
	}
	for _, col := range allColumns {
		if col.Name == "created_at" && !insertSet["created_at"] {
			m.HasAppCreatedAt = true
		}
		if col.Name == "updated_at" && !insertSet["updated_at"] {
			m.HasAppUpdatedAt = true
		}
	}
}

func buildUpdateStoreMethod(m *StoreMethod, rq ResolvedQuery, hasUpdatedAt bool) {
	for _, f := range rq.SetFields {
		m.UpdateFields = append(m.UpdateFields, StoreFieldInfo{
			GoName: f.GoName,
			DBName: f.DBName,
			IsTime: f.IsTime,
		})
	}

	if len(rq.Params) > 0 {
		m.WhereField = rq.Params[0]
		m.WhereGoVar = ToCamelCase(rq.Params[0])
	}
	m.HasUpdatedAt = hasUpdatedAt
}

func buildUpdateReturningStoreMethod(m *StoreMethod, rq ResolvedQuery, resolved *ResolvedFile, allCols string, hasUpdatedAt bool) {
	repoPkg := RepoPackage(resolved.TableName)

	buildUpdateStoreMethod(m, rq, hasUpdatedAt)

	if len(rq.ReturnFields) > 0 {
		m.ReturnTypeName = repoPkg + "." + rq.FuncName + "Result"
		m.UpdateReturningCols = joinFieldDBNames(rq.ReturnFields)
	} else {
		m.UpdateReturningCols = allCols
	}
}

func buildScanManyStoreMethod(m *StoreMethod, rq ResolvedQuery, resolved *ResolvedFile, allCols string) {
	m.SQL = buildNamedSQL(rq.QueryBlock, resolved.SchemaName, resolved.TableName, allCols)
	m.NamedArgs = buildNamedArgList(rq.Params)
}

func buildScanStoreMethod(m *StoreMethod, rq ResolvedQuery, resolved *ResolvedFile, allCols string) {
	repoPkg := RepoPackage(resolved.TableName)

	cols := allCols
	if m.Category == "scan_one_custom" {
		m.ReturnTypeName = repoPkg + "." + rq.FuncName + "Result"
		cols = joinFieldDBNames(rq.ReturnFields)
	}

	m.SQL = buildNamedSQL(rq.QueryBlock, resolved.SchemaName, resolved.TableName, cols)
	m.NamedArgs = buildNamedArgList(rq.Params)
}

func buildExecStoreMethod(m *StoreMethod, rq ResolvedQuery, resolved *ResolvedFile, allCols string) {
	m.ExecSQL = buildNamedSQL(rq.QueryBlock, resolved.SchemaName, resolved.TableName, allCols)
	m.ExecArgs = buildNamedArgList(rq.Params)
	if v, ok := rq.Annotations["check_rows"]; ok && v == "false" {
		m.SkipRowCheck = true
	}
}

// ─── method orchestrator ─────────────────────────────────────────────────────

func buildStoreMethod(
	rq ResolvedQuery,
	resolved *ResolvedFile,
	qualifiedTable, allCols string,
	hasUpdatedAt, hasSoftDelete bool,
) StoreMethod {
	repoPkg := RepoPackage(resolved.TableName)
	category := resolveCategory(rq)

	m := StoreMethod{
		FuncName: rq.FuncName,
		Category: category,
		Params:   buildStoreParamList(rq, repoPkg, resolved.EntityName, category),
		Returns:  buildStoreReturnType(rq, repoPkg, resolved.EntityName, category),
	}

	switch category {
	case "list":
		buildListStoreMethod(&m, rq, resolved, allCols)
	case "create":
		buildCreateStoreMethod(&m, rq, resolved.AllColumns)
	case "update":
		buildUpdateStoreMethod(&m, rq, hasUpdatedAt)
	case "update_returning":
		buildUpdateReturningStoreMethod(&m, rq, resolved, allCols, hasUpdatedAt)
	case "scan_many":
		buildScanManyStoreMethod(&m, rq, resolved, allCols)
	case "scan_one", "scan_one_custom":
		buildScanStoreMethod(&m, rq, resolved, allCols)
	case "exec":
		buildExecStoreMethod(&m, rq, resolved, allCols)
	}

	return m
}

// ─── SQL transformation helpers ──────────────────────────────────────────────

func buildNamedSQL(qb QueryBlock, schemaName, tableName, cols string) string {
	sql := strings.TrimSpace(qb.SQL)
	sql = strings.TrimRight(sql, ";")
	sql = strings.TrimSpace(sql)

	sql = qualifyTable(sql, schemaName, tableName)
	sql = expandSelectStar(sql, cols)
	sql = expandReturningStar(sql, cols)

	return sql
}

func qualifyTable(sql, schemaName, tableName string) string {
	qualified := schemaName + "." + tableName

	for _, prefix := range []string{"FROM ", "INTO ", "JOIN "} {
		sql = strings.Replace(sql, prefix+tableName, prefix+qualified, -1)
	}

	if strings.HasPrefix(strings.ToUpper(sql), "UPDATE ") {
		prefix := "UPDATE " + tableName
		if strings.HasPrefix(sql, prefix) {
			sql = "UPDATE " + qualified + sql[len(prefix):]
		}
	}

	if strings.HasPrefix(strings.ToUpper(sql), "DELETE FROM ") {
		prefix := "DELETE FROM " + tableName
		if strings.HasPrefix(sql, prefix) {
			sql = "DELETE FROM " + qualified + sql[len(prefix):]
		}
	}

	return sql
}

func expandSelectStar(sql, cols string) string {
	return strings.Replace(sql, "SELECT *", "SELECT "+cols, 1)
}

func expandReturningStar(sql, cols string) string {
	return strings.Replace(sql, "RETURNING *", "RETURNING "+cols, 1)
}

// buildListBaseSQL strips dynamic clauses ($order, $limit, $filters) from the
// query SQL, cleans up dangling WHERE/AND/OR, and qualifies the table name.
func buildListBaseSQL(qb QueryBlock, schemaName, tableName string) string {
	sql := strings.TrimSpace(qb.SQL)
	sql = strings.TrimRight(sql, ";")
	sql = stripDynamicClauses(sql)
	sql = cleanTrailingWhereAnd(sql)
	sql = qualifyTable(sql, schemaName, tableName)
	return sql
}

// cleanTrailingWhereAnd strips dangling WHERE, AND, or OR from the end of SQL
// after dynamic clause lines have been removed. Word-bounded to avoid matching
// column names like "demand".
func cleanTrailingWhereAnd(sql string) string {
	for {
		trimmed := strings.TrimSpace(sql)
		if trimmed == "" {
			return trimmed
		}
		upper := strings.ToUpper(trimmed)
		changed := false
		for _, suffix := range []string{"WHERE", "AND", "OR"} {
			if strings.HasSuffix(upper, suffix) {
				before := trimmed[:len(trimmed)-len(suffix)]
				// Word-boundary check: preceding char must be whitespace or start of string.
				if before == "" || before[len(before)-1] == ' ' || before[len(before)-1] == '\n' || before[len(before)-1] == '\t' {
					sql = strings.TrimSpace(before)
					changed = true
					break
				}
			}
		}
		if !changed {
			return sql
		}
	}
}

// stripDynamicClauses removes lines containing $order or $limit placeholders
// from SQL. These are replaced by dynamic Go code in the template.
// Named filter placeholders ($conditions, $search, etc.) are preserved in BaseSQL
// and substituted at runtime via replaceFilterPlaceholder.
func stripDynamicClauses(sql string) string {
	var lines []string
	for _, line := range strings.Split(sql, "\n") {
		upper := strings.ToUpper(strings.TrimSpace(line))
		if strings.Contains(upper, "$ORDER") || strings.Contains(upper, "$LIMIT") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ─── shared helpers ──────────────────────────────────────────────────────────

func buildNamedArgList(params []string) []NamedArg {
	var args []NamedArg
	for _, p := range params {
		args = append(args, NamedArg{
			ArgName: p,
			GoExpr:  ToCamelCase(p),
		})
	}
	return args
}

func joinFieldDBNames(fields []FieldInfo) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.DBName
	}
	return strings.Join(names, ", ")
}

func formatColList(cols []string) string {
	if len(cols) == 0 {
		return ""
	}
	var buf strings.Builder
	for i, col := range cols {
		if i > 0 {
			buf.WriteString(",\n\t\t\t")
		}
		buf.WriteString(col)
	}
	return buf.String()
}
