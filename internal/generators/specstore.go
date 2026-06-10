package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// specStorePackageSuffix is appended to the entity package name to form the
// dialect-neutral store package (users → usersstore).
const specStorePackageSuffix = "store"

// reSpecNamedParam matches a @named_param occurrence inside SQL.
var reSpecNamedParam = regexp.MustCompile(`@(\w+)`)

// ─── specstore template data types ───────────────────────────────────────────

// SpecFilterField is one filter-struct field mapped to crud predicates.
// Time fields additionally emit <Name>After (OpGT) and <Name>Before (OpLT).
type SpecFilterField struct {
	GoName string
	Col    string
	IsTime bool
}

// SpecSet is one column assignment in the Creates/Updates mappings.
type SpecSet struct {
	GoName string
	Col    string
}

// SpecOrderField is one entry in the OrderFields whitelist.
type SpecOrderField struct {
	Key       string
	Col       string
	CastLower bool
}

// SpecSearch describes the declared search strategy.
type SpecSearch struct {
	StrategyConst string // crud constant name: SearchContains, SearchWebSearch, SearchTSVector
	FieldsJoined  string // quoted, comma-joined searched columns (contains, web_search)
	Column        string // precomputed tsvector column (tsvector)
}

// SpecRecordStateMethod is a named record-state transition wrapper
// (SoftDelete/Archive/Restore) over the generic SetRecordState.
type SpecRecordStateMethod struct {
	Name    string
	State   string
	PKParam string // camelCase primary key parameter name
}

// SpecCustomMethod is a custom @func generated as literal SQL on the store.
type SpecCustomMethod struct {
	Name         string
	Params       string // "ctx context.Context, email string"
	Category     string // "scan_one" or "exec"
	QueryExpr    string // Go expression building the SQL with args.Add(...) calls
	SkipRowCheck bool   // exec only: from @check_rows: false
}

// SpecSkippedFunc is a queries.sql func the generator could not translate.
// It is surfaced as a TODO comment so the developer hand-writes it in the
// bootstrap store.go (rung three of the escape ladder).
type SpecSkippedFunc struct {
	Name   string
	Reason string
}

// SpecStoreData holds everything needed to render the specstore templates.
type SpecStoreData struct {
	FrameworkPath string
	PackageName   string
	RepoPkg       string
	RepoImport    string
	EntityName    string

	Table         string
	PK            string
	ColumnsJoined string // quoted, comma-joined column list

	// Fully-qualified generic type arguments, e.g.
	// "[users.User, users.FilterList, users.CreateUser, users.UpdateUser]".
	SpecTypeArgs   string
	FilterTypeExpr string
	CreateTypeExpr string
	UpdateTypeExpr string

	FilterFields    []SpecFilterField
	HasFilterStruct bool // filter type exists → AuthorizedIDs accessor
	HasSearch       bool // filter struct carries SearchTerm → Search + SearchTerm
	Search          SpecSearch

	CreateSets   []SpecSet
	UpdateFields []SpecSet

	// UpdateKeyParams, when set, marks a composite-key entity: a wrapper
	// Update is emitted shadowing the generic single-key verb, delegating
	// to crud.Store.UpdateBy with one KeyVal per entry (query param order).
	UpdateKeyParams []SpecKeyParam

	HasCreatedAt        bool
	HasUpdatedAt        bool
	AutoNowCreateJoined string // quoted, comma-joined AutoNowCreate columns
	RecordStateCol      string

	OrderFields  []SpecOrderField
	DefaultOrder string

	RecordStateMethods []SpecRecordStateMethod
	CustomMethods      []SpecCustomMethod
	Skipped            []SpecSkippedFunc

	NeedsTime bool // any custom method encodes a time.Time argument
	HasCustom bool // emit the mapErr helper
}

// GenerateSpecStore produces the dialect-neutral <entity>store package next to
// the entity repository: generated.go (always overwritten) and store.go
// (bootstrap, created once — holds hand-written methods and the TxRunner
// pattern documentation).
func GenerateSpecStore(resolved *ResolvedFile, repoDir, modulePath string, opts Options) error {
	data, err := BuildSpecStoreData(resolved, modulePath)
	if err != nil {
		return fmt.Errorf("build specstore data for %s: %w", resolved.TableName, err)
	}

	outDir := filepath.Join(repoDir, data.PackageName)
	if err := ensureDir(outDir, opts); err != nil {
		return fmt.Errorf("create dir %s: %w", outDir, err)
	}

	// Skip methods the developer has customized in the bootstrap store.go.
	bootstrapPath := filepath.Join(outDir, "store.go")
	if fileExists(bootstrapPath) {
		if err := dropCustomizedSpecMethods(&data, bootstrapPath); err != nil {
			return err
		}
	}

	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	genFiles := []genFile{
		{"generated.go", specStoreGeneratedTemplate, false},
		{"store.go", specStoreBootstrapTemplate, true},
	}

	for _, f := range genFiles {
		path := filepath.Join(outDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderSpecStoreTemplate(f.tmpl, data)
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

// dropCustomizedSpecMethods removes generated methods that the developer has
// already defined on Store in the bootstrap file, mirroring the repository
// generator's customized-method skip.
func dropCustomizedSpecMethods(data *SpecStoreData, bootstrapPath string) error {
	var keptWrappers []SpecRecordStateMethod
	for _, m := range data.RecordStateMethods {
		exists, err := MethodExistsOnType(bootstrapPath, "Store", m.Name)
		if err != nil {
			return fmt.Errorf("check custom method %s: %w", m.Name, err)
		}
		if exists {
			fmt.Printf("      skip method %s (customized in store.go)\n", m.Name)
			continue
		}
		keptWrappers = append(keptWrappers, m)
	}
	data.RecordStateMethods = keptWrappers

	var keptCustom []SpecCustomMethod
	for _, m := range data.CustomMethods {
		exists, err := MethodExistsOnType(bootstrapPath, "Store", m.Name)
		if err != nil {
			return fmt.Errorf("check custom method %s: %w", m.Name, err)
		}
		if exists {
			fmt.Printf("      skip method %s (customized in store.go)\n", m.Name)
			continue
		}
		keptCustom = append(keptCustom, m)
	}
	data.CustomMethods = keptCustom
	recomputeSpecImports(data)
	return nil
}

func renderSpecStoreTemplate(tmplText string, data SpecStoreData) ([]byte, error) {
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

// ─── data building ────────────────────────────────────────────────────────────

// BuildSpecStoreData derives the dialect-neutral store description from a
// resolved queries.sql file. Standard verbs (List/Get/Create/Update/Delete and
// record-state transitions) fold into the crud.Spec; remaining funcs become
// literal-SQL methods, and anything the builder cannot confidently translate
// is surfaced as a skip TODO for the bootstrap store.go.
func BuildSpecStoreData(resolved *ResolvedFile, modulePath string) (SpecStoreData, error) {
	repoPkg := RepoPackage(resolved.TableName)
	repoImport := modulePath + "/core/repositories/"
	if resolved.DomainName != "" {
		repoImport += resolved.DomainName + "/"
	}
	repoImport += repoPkg

	data := SpecStoreData{
		FrameworkPath: gopernicusFrameworkPath,
		PackageName:   StorePackage(resolved.TableName, specStorePackageSuffix),
		RepoPkg:       repoPkg,
		RepoImport:    repoImport,
		EntityName:    resolved.EntityName,
		Table:         resolved.TableName, // no schema prefix — dialect-neutral
		PK:            resolved.PKColumn,
	}

	hasRecordState := false
	textColumns := map[string]bool{}
	var colNames []string
	for _, col := range resolved.AllColumns {
		colNames = append(colNames, col.Name)
		switch col.Name {
		case "record_state":
			hasRecordState = true
		case "created_at":
			data.HasCreatedAt = true
		case "updated_at":
			data.HasUpdatedAt = true
		}
		if strings.TrimPrefix(col.GoType, "*") == "string" && !col.IsEnum {
			textColumns[col.Name] = true
		}
	}
	data.ColumnsJoined = quoteJoin(colNames)
	if hasRecordState {
		data.RecordStateCol = "record_state"
	}

	var autoNowCreate []string
	if data.HasCreatedAt {
		autoNowCreate = append(autoNowCreate, "created_at")
	}
	if data.HasUpdatedAt {
		autoNowCreate = append(autoNowCreate, "updated_at")
	}
	data.AutoNowCreateJoined = quoteJoin(autoNowCreate)

	filterType := ""
	createSeen := map[string]bool{}
	updateSeen := map[string]bool{}
	hasUpdateQuery := false

	for _, rq := range resolved.Queries {
		category := resolveCategory(rq)

		switch category {
		case "list":
			if rq.FuncName != "List" || !rq.HasOrder {
				data.Skipped = append(data.Skipped, SpecSkippedFunc{
					Name:   rq.FuncName,
					Reason: "list-shaped query not covered by the generic List verb",
				})
				continue
			}
			if rq.HasFilters || (rq.HasSearch && len(rq.SearchFields) > 0) {
				filterType = "Filter" + rq.FuncName
			} else {
				data.Skipped = append(data.Skipped, SpecSkippedFunc{
					Name:   rq.FuncName,
					Reason: "list without a filter struct does not match the generic List signature",
				})
				continue
			}
			buildSpecListData(&data, rq, textColumns, resolved.PKColumn)

		case "create":
			if rq.EventOutbox {
				data.Skipped = append(data.Skipped, SpecSkippedFunc{Name: rq.FuncName, Reason: "uses the transactional outbox"})
				continue
			}
			if rq.FuncName != "Create" {
				data.Skipped = append(data.Skipped, SpecSkippedFunc{Name: rq.FuncName, Reason: "non-standard insert; hand-write or rename to Create"})
				continue
			}
			for _, f := range rq.InsertFields {
				if !createSeen[f.DBName] {
					createSeen[f.DBName] = true
					data.CreateSets = append(data.CreateSets, SpecSet{GoName: f.GoName, Col: f.DBName})
				}
			}

		case "update", "update_returning":
			if rq.EventOutbox {
				data.Skipped = append(data.Skipped, SpecSkippedFunc{Name: rq.FuncName, Reason: "uses the transactional outbox"})
				continue
			}
			singleKey := specParamsArePK(rq, resolved.PKColumn)
			compositeKey := specParamsAreCompositePK(rq, resolved)
			if rq.FuncName != "Update" || (!singleKey && !compositeKey) || len(rq.ReturnFields) > 0 {
				data.Skipped = append(data.Skipped, SpecSkippedFunc{Name: rq.FuncName, Reason: "non-standard update; hand-write in store.go"})
				continue
			}
			hasUpdateQuery = true
			if compositeKey {
				// The generic Update verb is single-key; emit a wrapper that
				// shadows it, delegating to UpdateBy with every key column.
				for _, p := range rq.Params {
					goType := "string"
					if t, ok := rq.ParamTypes[p]; ok {
						goType = t
					}
					data.UpdateKeyParams = append(data.UpdateKeyParams, SpecKeyParam{
						Col:    p,
						GoVar:  ToCamelCase(p),
						GoType: goType,
					})
				}
			}
			for _, f := range rq.SetFields {
				if !updateSeen[f.DBName] {
					updateSeen[f.DBName] = true
					data.UpdateFields = append(data.UpdateFields, SpecSet{GoName: f.GoName, Col: f.DBName})
				}
			}

		case "scan_one":
			if rq.FuncName == "Get" && specParamsArePK(rq, resolved.PKColumn) {
				continue // covered by the generic Get verb
			}
			buildSpecCustomMethod(&data, rq, resolved, "scan_one")

		case "scan_one_custom", "scan_many":
			data.Skipped = append(data.Skipped, SpecSkippedFunc{
				Name:   rq.FuncName,
				Reason: "returns a custom projection; hand-write in store.go",
			})

		case "exec":
			if rq.EventOutbox {
				data.Skipped = append(data.Skipped, SpecSkippedFunc{Name: rq.FuncName, Reason: "uses the transactional outbox"})
				continue
			}
			if rq.FuncName == "Delete" && rq.Type == QueryDelete && specParamsArePK(rq, resolved.PKColumn) {
				continue // covered by the generic Delete verb
			}
			if hasRecordState {
				if state, ok := specRecordStateTransition(rq, resolved.TableName, resolved.PKColumn); ok {
					data.RecordStateMethods = append(data.RecordStateMethods, SpecRecordStateMethod{
						Name:    rq.FuncName,
						State:   state,
						PKParam: ToCamelCase(resolved.PKColumn),
					})
					continue
				}
			}
			buildSpecCustomMethod(&data, rq, resolved, "exec")
		}
	}

	// Type arguments for the crud.Spec generic.
	entityType := repoPkg + "." + resolved.EntityName
	data.FilterTypeExpr = "struct{}"
	if filterType != "" {
		data.FilterTypeExpr = repoPkg + "." + filterType
		data.HasFilterStruct = true
	}

	// Creates: from the insert query when present. Otherwise fall back to the
	// framework convention — every column except the audit timestamps — which
	// matches hand-written Create<Entity> inputs (e.g. a transactional Create
	// kept in the bootstrap store.go).
	if len(data.CreateSets) == 0 {
		for _, col := range resolved.AllColumns {
			if col.Name == "created_at" || col.Name == "updated_at" {
				continue
			}
			data.CreateSets = append(data.CreateSets, SpecSet{GoName: ToPascalCase(col.Name), Col: col.Name})
		}
	}
	data.CreateTypeExpr = repoPkg + ".Create" + resolved.EntityName

	data.UpdateTypeExpr = "struct{}"
	if hasUpdateQuery {
		data.UpdateTypeExpr = repoPkg + ".Update" + resolved.EntityName
	}

	data.SpecTypeArgs = "[" + entityType + ", " + data.FilterTypeExpr + ", " + data.CreateTypeExpr + ", " + data.UpdateTypeExpr + "]"

	recomputeSpecImports(&data)
	return data, nil
}

// recomputeSpecImports derives the conditional import flags from the methods
// that remain after customized-method filtering.
func recomputeSpecImports(data *SpecStoreData) {
	data.HasCustom = len(data.CustomMethods) > 0
	data.NeedsTime = false
	for _, m := range data.CustomMethods {
		if strings.Contains(m.Params, "time.Time") {
			data.NeedsTime = true
		}
	}
}

// buildSpecListData fills the filter, search, and ordering parts of the spec
// from the entity's List query.
func buildSpecListData(data *SpecStoreData, rq ResolvedQuery, textColumns map[string]bool, pkColumn string) {
	for _, f := range rq.AllFilterFields() {
		data.FilterFields = append(data.FilterFields, SpecFilterField{
			GoName: f.GoName,
			Col:    f.DBName,
			IsTime: f.IsTime,
		})
	}

	if rq.HasSearch && len(rq.SearchFields) > 0 {
		data.HasSearch = true
		var fields []string
		for _, f := range rq.SearchFields {
			fields = append(fields, f.DBName)
		}
		switch rq.SearchType {
		case "web_search":
			data.Search = SpecSearch{StrategyConst: "SearchWebSearch", FieldsJoined: quoteJoin(fields)}
		case "tsvector":
			data.Search = SpecSearch{StrategyConst: "SearchTSVector", Column: fields[0]}
		default: // ilike
			data.Search = SpecSearch{StrategyConst: "SearchContains", FieldsJoined: quoteJoin(fields)}
		}
	}

	orderSeen := map[string]bool{}
	for _, o := range rq.OrderFields {
		if orderSeen[o.DBColumn] {
			continue
		}
		orderSeen[o.DBColumn] = true
		data.OrderFields = append(data.OrderFields, SpecOrderField{
			Key:       o.DBColumn,
			Col:       o.DBColumn,
			CastLower: o.DBColumn != pkColumn && textColumns[o.DBColumn],
		})
	}

	switch {
	case orderSeen["created_at"]:
		data.DefaultOrder = "created_at"
	case orderSeen[pkColumn]:
		data.DefaultOrder = pkColumn
	case len(data.OrderFields) > 0:
		data.DefaultOrder = data.OrderFields[0].Key
	}
}

// buildSpecCustomMethod translates a custom @func into a literal-SQL store
// method, or records a skip when the SQL is beyond the translator (multiple
// statements, CTEs, dynamic placeholders).
func buildSpecCustomMethod(data *SpecStoreData, rq ResolvedQuery, resolved *ResolvedFile, category string) {
	if reason, ok := specUntranslatable(rq); ok {
		data.Skipped = append(data.Skipped, SpecSkippedFunc{Name: rq.FuncName, Reason: reason})
		return
	}

	colList := strings.Join(specColumnNames(resolved.AllColumns), ", ")
	queryExpr, err := buildSpecQueryExpr(rq, resolved.SchemaName, colList)
	if err != nil {
		data.Skipped = append(data.Skipped, SpecSkippedFunc{Name: rq.FuncName, Reason: err.Error()})
		return
	}

	params := []string{"ctx context.Context"}
	for _, p := range rq.Params {
		goType := "string"
		if t, ok := rq.ParamTypes[p]; ok {
			goType = t
		}
		params = append(params, ToCamelCase(p)+" "+goType)
	}

	skipRowCheck := rq.Annotations["check_rows"] == "false"

	data.CustomMethods = append(data.CustomMethods, SpecCustomMethod{
		Name:         rq.FuncName,
		Params:       strings.Join(params, ", "),
		Category:     category,
		QueryExpr:    queryExpr,
		SkipRowCheck: skipRowCheck,
	})
}

// specUntranslatable reports whether a custom func's SQL is beyond the
// literal-SQL translator.
func specUntranslatable(rq ResolvedQuery) (string, bool) {
	sql := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(rq.SQL), ";"))
	upper := strings.ToUpper(sql)
	switch {
	case strings.Contains(sql, ";"):
		return "multiple SQL statements; hand-write in store.go", true
	case strings.HasPrefix(upper, "WITH"):
		return "uses a CTE; hand-write in store.go", true
	case strings.Contains(sql, "$"):
		return "uses dynamic $ placeholders; hand-write in store.go", true
	}
	return "", false
}

// buildSpecQueryExpr converts @named-arg SQL into a Go string-concatenation
// expression that binds arguments positionally via args.Add, in SQL-occurrence
// order. time.Time parameters route through the dialect's TimeArg encoding.
func buildSpecQueryExpr(rq ResolvedQuery, schemaName, colList string) (string, error) {
	sql := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(rq.SQL), ";"))

	// Dialect-neutral: drop schema qualification.
	sql = strings.ReplaceAll(sql, schemaName+".", "")

	sql = expandSelectStar(sql, colList)
	sql = expandReturningStar(sql, colList)

	// Collapse to a single line.
	sql = strings.Join(strings.Fields(sql), " ")

	var parts []string
	last := 0
	for _, loc := range reSpecNamedParam.FindAllStringSubmatchIndex(sql, -1) {
		if seg := sql[last:loc[0]]; seg != "" {
			parts = append(parts, strconv.Quote(seg))
		}
		name := sql[loc[2]:loc[3]]
		expr := ToCamelCase(name)
		if rq.ParamTypes[name] == "time.Time" {
			expr = "s.Dialect().TimeArg(" + expr + ")"
		}
		parts = append(parts, "args.Add("+expr+")")
		last = loc[1]
	}
	if seg := sql[last:]; seg != "" {
		parts = append(parts, strconv.Quote(seg))
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("empty SQL")
	}
	return strings.Join(parts, " + "), nil
}

// specRecordStateTransition matches an exec query of the exact shape
// `UPDATE <table> SET record_state = '<state>' WHERE <pk> = @<pk>` and
// returns the target state.
func specRecordStateTransition(rq ResolvedQuery, tableName, pkColumn string) (string, bool) {
	if len(rq.Params) != 1 || rq.Params[0] != pkColumn {
		return "", false
	}
	sql := strings.Join(strings.Fields(strings.TrimRight(strings.TrimSpace(rq.SQL), ";")), " ")
	re := regexp.MustCompile(`(?i)^UPDATE (?:\w+\.)?` + regexp.QuoteMeta(tableName) +
		` SET record_state = '(\w+)' WHERE ` + regexp.QuoteMeta(pkColumn) +
		` = @` + regexp.QuoteMeta(pkColumn) + `$`)
	m := re.FindStringSubmatch(sql)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// specParamsArePK reports whether the query's only named parameter is the
// primary key column.
func specParamsArePK(rq ResolvedQuery, pkColumn string) bool {
	return len(rq.Params) == 1 && rq.Params[0] == pkColumn
}

// SpecKeyParam is one key column of a composite-key Update wrapper: the db
// column, the Go parameter name, and its Go type.
type SpecKeyParam struct {
	Col    string
	GoVar  string
	GoType string
}

// specParamsAreCompositePK reports whether the query's params are exactly the
// table's composite primary key columns (any order; the param order drives
// the generated signature).
func specParamsAreCompositePK(rq ResolvedQuery, resolved *ResolvedFile) bool {
	if resolved.Table == nil || resolved.Table.PrimaryKey == nil {
		return false
	}
	pkCols := resolved.Table.PrimaryKey.Columns
	if len(pkCols) < 2 || len(rq.Params) != len(pkCols) {
		return false
	}
	want := make(map[string]bool, len(pkCols))
	for _, c := range pkCols {
		want[c] = true
	}
	for _, p := range rq.Params {
		if !want[p] {
			return false
		}
	}
	return true
}

func specColumnNames(cols []schema.ColumnInfo) []string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return names
}

// quoteJoin renders a Go string-slice body: `"a", "b", "c"`.
func quoteJoin(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	return strings.Join(quoted, ", ")
}
