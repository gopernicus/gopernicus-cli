package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// ─── fixture template data types ────────────────────────────────────────────

// ParentFixture describes a FK dependency on another entity.
type ParentFixture struct {
	VarName                string // camelCase param name, e.g. "serviceAccountID"
	EntityName             string // PascalCase parent entity, e.g. "ServiceAccount"
	TableName              string // parent table name, e.g. "service_accounts"
	FKColumn               string // FK column on this table, e.g. "service_account_id"
	PKGoName               string // parent's PK Go field, e.g. "ServiceAccountID"
	IsSelfReference        bool   // FK references same table
	IsPrincipalInheritance bool   // PK is FK to principals table
	IsInBatch              bool   // parent table has a fixture in this generation batch
	ForwardParams          []ParentFixture // external params to forward when calling this in-batch parent's WithDefaults
}

// FixtureEntity describes a single entity for fixture generation.
// pkIsUUID reports whether the resolved entity's primary key is a uuid
// column. uuid PKs need GenerateUUID — GenerateID's URL-safe alphabet is
// rejected by the uuid type.
func pkIsUUID(resolved *ResolvedFile) bool {
	for _, col := range resolved.AllColumns {
		if col.Name == resolved.PKColumn {
			return strings.EqualFold(col.DBType, "uuid")
		}
	}
	return false
}

type FixtureEntity struct {
	EntityName  string // PascalCase singular, e.g. "User"
	EntityLower string // lowercase singular, e.g. "user"
	TableName   string // raw table name, e.g. "users"
	DomainName  string // e.g. "auth"
	RepoPkg     string // e.g. "users"
	RepoImport  string // full import path

	PKColumn   string // e.g. "user_id"
	PKGoName   string // e.g. "UserID"
	PKGoType   string // e.g. "string"
	PKIsFK     bool   // true if PK is also a FK (use param, don't generate)
	PKIsUUID   bool   // true when the PK column is a uuid — GenerateID's alphabet is rejected

	// InsertFields are the columns the fixture INSERT will populate.
	InsertFields []FixtureField

	// AllColumns are every column on the table (for the SELECT back).
	AllColumns []FixtureField

	// ParentFixtures are FK dependencies (other entities that must exist first).
	ParentFixtures []ParentFixture

	// AllExternalParams is the transitive closure of out-of-batch FK params:
	// own out-of-batch parents + all in-batch ancestors' out-of-batch parents.
	// These become parameters on the WithDefaults signature.
	AllExternalParams []ParentFixture

	// HasPrincipalInheritance is true if PK is a FK to the principals table.
	HasPrincipalInheritance bool
}

// FixtureField describes a single column for fixture generation.
type FixtureField struct {
	GoName       string // PascalCase, e.g. "Email"
	GoType       string // e.g. "string", "*time.Time"
	DBName       string // e.g. "email"
	DBType       string // e.g. "uuid", "varchar(255)"
	GoImport     string // e.g. "time"
	IsEnum       bool
	IsNullable   bool
	HasDefault   bool
	MaxLength    int
	IsForeignKey bool // true if this column is a FK

	// TestDefault is the Go expression for a sensible test default.
	TestDefault string
}

// FixtureTemplateData holds all data for rendering the fixtures file.
type FixtureTemplateData struct {
	ModulePath    string
	FrameworkPath string // gopernicus framework module path (for sdk, infra imports)
	Entities      []FixtureEntity
	Imports       []string // deduplicated extra imports
}

// BuildFixtureEntity creates a FixtureEntity from a ResolvedFile.
func BuildFixtureEntity(resolved *ResolvedFile, modulePath string) FixtureEntity {
	entity := FixtureEntity{
		EntityName:  resolved.EntityName,
		EntityLower: resolved.EntityLower,
		TableName:   resolved.TableName,
		DomainName:  resolved.DomainName,
		RepoPkg:     resolved.PackageName,
		RepoImport:  modulePath + "/core/repositories/" + resolved.DomainName + "/" + resolved.PackageName,
		PKColumn:    resolved.PKColumn,
		PKGoName:    resolved.PKGoName,
		PKGoType:    resolved.PKGoType,
		PKIsUUID:    pkIsUUID(resolved),
	}

	// Build FK parent fixtures from the table's foreign keys.
	if resolved.Table != nil {
		entity.ParentFixtures = buildParentFixtures(resolved.Table)
		for _, p := range entity.ParentFixtures {
			if p.IsPrincipalInheritance {
				entity.HasPrincipalInheritance = true
			}
			// PK is a FK only if it's NOT principal inheritance
			// (principal inheritance generates the PK itself).
			if p.FKColumn == resolved.PKColumn && !p.IsPrincipalInheritance {
				entity.PKIsFK = true
			}
		}
	}

	// Build a set of FK column names for quick lookup.
	// Skip self-referential FKs — they won't be params on the function signature.
	fkColumns := make(map[string]string) // FK column → parent var name
	selfRefNullableCols := make(map[string]bool)
	for _, p := range entity.ParentFixtures {
		if p.IsSelfReference {
			selfRefNullableCols[p.FKColumn] = true
			continue
		}
		fkColumns[p.FKColumn] = p.VarName
	}

	// Find the Create query's InsertFields.
	for _, rq := range resolved.Queries {
		if rq.Type == QueryInsert && len(rq.InsertFields) > 0 {
			for _, f := range rq.InsertFields {
				ff := fieldToFixture(f, resolved.AllColumns)
				// Mark FK fields and set their TestDefault to the param variable.
				if varName, ok := fkColumns[ff.DBName]; ok {
					ff.IsForeignKey = true
					ff.TestDefault = varName
				}
				// Self-referential nullable FKs default to nil (create root rows).
				if selfRefNullableCols[ff.DBName] && ff.IsNullable {
					ff.TestDefault = "nil"
				}
				entity.InsertFields = append(entity.InsertFields, ff)
			}
			break
		}
	}

	// If no Create query, build from all non-defaulted columns.
	if len(entity.InsertFields) == 0 {
		for _, col := range resolved.AllColumns {
			if col.Name == "created_at" || col.Name == "updated_at" {
				continue
			}
			ff := columnToFixture(col)
			if varName, ok := fkColumns[ff.DBName]; ok {
				ff.IsForeignKey = true
				ff.TestDefault = varName
			}
			// Self-referential nullable FKs default to nil (create root rows).
			if selfRefNullableCols[ff.DBName] && ff.IsNullable {
				ff.TestDefault = "nil"
			}
			entity.InsertFields = append(entity.InsertFields, ff)
		}
	}

	// Override PK field's TestDefault to use the generated variable.
	pkCamel := ToCamelCase(resolved.PKColumn)
	for i, f := range entity.InsertFields {
		if f.DBName == resolved.PKColumn {
			entity.InsertFields[i].TestDefault = pkCamel
			break
		}
	}

	// Honor CHECK constraints with enum-style allowed-value lists
	// (e.g. CHECK (principal_type IN ('user', 'service_account'))).
	// Without this, the generic test-default `"test_<col>"` violates the
	// constraint at INSERT time. The first allowed value wins.
	if resolved.Table != nil {
		allowed := allowedValuesFromCheckConstraints(resolved.Table.Constraints)
		if len(allowed) > 0 {
			for i, f := range entity.InsertFields {
				if vals, ok := allowed[f.DBName]; ok && len(vals) > 0 {
					// Only override non-FK string fields. FK columns get a
					// fixture-provided variable name and shouldn't be coerced
					// to a literal.
					if !f.IsForeignKey {
						entity.InsertFields[i].TestDefault = fmt.Sprintf("%q", vals[0])
					}
				}
			}
		}
	}

	// Native enum types (pg CREATE TYPE ... AS ENUM) carry their labels on
	// the reflected column rather than a CHECK constraint — same coercion,
	// first label wins.
	for i, f := range entity.InsertFields {
		if f.IsForeignKey {
			continue
		}
		for _, col := range resolved.AllColumns {
			if col.Name == f.DBName && col.IsEnum && len(col.EnumValues) > 0 {
				entity.InsertFields[i].TestDefault = fmt.Sprintf("%q", col.EnumValues[0])
				break
			}
		}
	}

	// Build AllColumns for SELECT back.
	for _, col := range resolved.AllColumns {
		entity.AllColumns = append(entity.AllColumns, columnToFixture(col))
	}

	return entity
}

// checkArrayValueRe pulls single-quoted string literals out of an `ARRAY[...]`
// inside a CHECK constraint's definition string. Postgres canonicalizes
// `col IN ('a', 'b')` to `(col)::text = ANY ((ARRAY['a'::varchar, 'b'::varchar])::text[])`
// so we cannot rely on the source `IN (...)` syntax surviving reflection.
var checkArrayValueRe = regexp.MustCompile(`'([^']*)'`)

// allowedValuesFromCheckConstraints returns a column → []value map for any
// single-column CHECK constraint that pins the column to a finite set of
// string literals. Two canonical shapes are recognized:
//
//   - `col IN ('a', 'b')` → `(col)::text = ANY ((ARRAY['a'::varchar, 'b'::varchar])::text[])`
//   - `col = 'v'`         → `(col)::text = 'v'::text`
//
// Range, regexp, and multi-column checks pass through unmodified — callers
// should treat them as "no override needed."
func allowedValuesFromCheckConstraints(constraints []schema.ConstraintInfo) map[string][]string {
	out := make(map[string][]string)
	for _, c := range constraints {
		if !strings.EqualFold(c.Type, "CHECK") {
			continue
		}
		if len(c.Columns) != 1 {
			continue
		}
		def := c.Definition

		// Shape 1: ARRAY[...] (IN-list form).
		if start := strings.Index(def, "ARRAY["); start >= 0 {
			if end := strings.Index(def[start:], "]"); end > 0 {
				segment := def[start : start+end+1]
				matches := checkArrayValueRe.FindAllStringSubmatch(segment, -1)
				if len(matches) > 0 {
					vals := make([]string, 0, len(matches))
					for _, m := range matches {
						vals = append(vals, m[1])
					}
					out[c.Columns[0]] = vals
					continue
				}
			}
		}

		// Shape 2: single-value equality. Recognized only when the
		// constraint contains exactly one quoted literal — otherwise
		// we can't tell apart `col = 'v'` from cases where multiple
		// literals appear as type tags or unrelated subexpressions.
		matches := checkArrayValueRe.FindAllStringSubmatch(def, -1)
		if len(matches) == 1 {
			out[c.Columns[0]] = []string{matches[0][1]}
		}
	}
	return out
}

// buildParentFixtures extracts FK dependencies from a table.
func buildParentFixtures(table *schema.TableInfo) []ParentFixture {
	var parents []ParentFixture

	// Detect principal inheritance: PK is a FK to principals.
	pkCol := ""
	if table.PrimaryKey != nil {
		pkCol = table.PrimaryKey.Column
	}

	for _, fk := range table.ForeignKeys {
		col := fk.ColumnName
		if len(fk.Columns) > 0 {
			col = fk.Columns[0]
		}
		if col == "" {
			continue
		}

		refCol := col
		if len(fk.RefColumns) > 0 {
			refCol = fk.RefColumns[0]
		}

		isSelfRef := fk.RefTable == table.TableName
		isPrincipalInheritance := col == pkCol && fk.RefTable == "principals"

		// Build var name: strip _id suffix, camelCase, add ID back.
		varName := ToCamelCase(col)

		parents = append(parents, ParentFixture{
			VarName:                varName,
			EntityName:             ToPascalCase(Singularize(fk.RefTable)),
			TableName:              fk.RefTable,
			FKColumn:               col,
			PKGoName:               ToPascalCase(refCol),
			IsSelfReference:        isSelfRef,
			IsPrincipalInheritance: isPrincipalInheritance,
		})
	}

	return parents
}

// ─── topological sort ───────────────────────────────────────────────────────

// buildDependencyGraph creates table → dependencies mapping.
func buildDependencyGraph(entities []FixtureEntity) map[string][]string {
	graph := make(map[string][]string)
	for _, e := range entities {
		if _, ok := graph[e.TableName]; !ok {
			graph[e.TableName] = nil
		}
		for _, p := range e.ParentFixtures {
			if p.IsSelfReference {
				continue
			}
			graph[e.TableName] = append(graph[e.TableName], p.TableName)
		}
	}
	return graph
}

// topologicalSortEntities orders entities so parents come before children.
func topologicalSortEntities(entities []FixtureEntity, graph map[string][]string) ([]FixtureEntity, error) {
	entityMap := make(map[string]*FixtureEntity)
	for i := range entities {
		entityMap[entities[i].TableName] = &entities[i]
	}

	visited := make(map[string]bool)
	inProgress := make(map[string]bool)
	var sorted []FixtureEntity

	var visit func(string) error
	visit = func(tableName string) error {
		if inProgress[tableName] {
			return fmt.Errorf("circular dependency detected involving table %s", tableName)
		}
		if visited[tableName] {
			return nil
		}
		inProgress[tableName] = true

		for _, dep := range graph[tableName] {
			if err := visit(dep); err != nil {
				return err
			}
		}

		visited[tableName] = true
		inProgress[tableName] = false

		if e, ok := entityMap[tableName]; ok {
			sorted = append(sorted, *e)
		}
		return nil
	}

	// Visit in deterministic order.
	tableNames := make([]string, 0, len(graph))
	for t := range graph {
		tableNames = append(tableNames, t)
	}
	sort.Strings(tableNames)

	for _, t := range tableNames {
		if err := visit(t); err != nil {
			return nil, err
		}
	}

	return sorted, nil
}

// ─── transitive external params ────────────────────────────────────────────

// computeTransitiveExternalParams walks the topo-sorted entities (parents first)
// and computes AllExternalParams for each entity: own out-of-batch parents plus
// all in-batch ancestors' out-of-batch parents. It also sets ForwardParams on
// each in-batch ParentFixture so the template knows what to pass.
func computeTransitiveExternalParams(entities []FixtureEntity) {
	entityByTable := make(map[string]*FixtureEntity, len(entities))
	for i := range entities {
		entityByTable[entities[i].TableName] = &entities[i]
	}

	for i := range entities {
		e := &entities[i]
		seen := make(map[string]bool)

		// Own out-of-batch parents first.
		for _, p := range e.ParentFixtures {
			if !p.IsSelfReference && !p.IsPrincipalInheritance && !p.IsInBatch {
				if !seen[p.VarName] {
					seen[p.VarName] = true
					e.AllExternalParams = append(e.AllExternalParams, p)
				}
			}
		}

		// Inherit from in-batch parents (already computed since topo-sorted).
		for j, p := range e.ParentFixtures {
			if !p.IsSelfReference && !p.IsPrincipalInheritance && p.IsInBatch {
				if parent, ok := entityByTable[p.TableName]; ok {
					e.ParentFixtures[j].ForwardParams = parent.AllExternalParams
					for _, tp := range parent.AllExternalParams {
						if !seen[tp.VarName] {
							seen[tp.VarName] = true
							e.AllExternalParams = append(e.AllExternalParams, tp)
						}
					}
				}
			}
		}
	}
}

// ─── generation ─────────────────────────────────────────────────────────────

// GenerateFixtures produces the test fixtures file for all entities in a domain.
func GenerateFixtures(data FixtureTemplateData, fixtureDir string, opts Options) error {
	if len(data.Entities) == 0 {
		return nil
	}

	// Mark which parent FK tables have a fixture in this batch.
	batchTables := make(map[string]bool, len(data.Entities))
	for _, e := range data.Entities {
		batchTables[e.TableName] = true
	}
	for i := range data.Entities {
		for j := range data.Entities[i].ParentFixtures {
			data.Entities[i].ParentFixtures[j].IsInBatch = batchTables[data.Entities[i].ParentFixtures[j].TableName]
		}
	}

	// Topological sort: parents before children.
	graph := buildDependencyGraph(data.Entities)
	sorted, err := topologicalSortEntities(data.Entities, graph)
	if err != nil {
		return err
	}
	data.Entities = sorted

	// Compute transitive external params bottom-up (topo order: parents first).
	computeTransitiveExternalParams(data.Entities)

	// Collect unique imports.
	data.Imports = collectFixtureImports(data.Entities)

	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	genFiles := []genFile{
		{"generated.go", fixtureGeneratedTemplate, false},
		{"fixtures.go", fixtureBootstrapTemplate, true},
	}

	for _, f := range genFiles {
		path := filepath.Join(fixtureDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderFixtureTemplate(f.tmpl, data)
		if err != nil {
			return fmt.Errorf("render %s: %w", f.name, err)
		}

		formatted, err := format.Source(out)
		if err != nil {
			_ = writeFile(path, out, opts)
			return fmt.Errorf("go/format %s: %w\nUnformatted output written for debugging.", f.name, err)
		}

		if err := writeFile(path, formatted, opts); err != nil {
			return err
		}
	}

	return nil
}

// ─── template rendering ─────────────────────────────────────────────────────

func renderFixtureTemplate(tmplStr string, data FixtureTemplateData) ([]byte, error) {
	funcMap := template.FuncMap{
		"lower":          strings.ToLower,
		"camel":          ToCamelCase,
		"singularize":    Singularize,
		"join":           strings.Join,
		"positionalArgs": positionalArgs,
		"add":            func(a, b int) int { return a + b },
		"insertCols":     insertCols,
		"selectCols":     selectCols,
		// nonSelfRefParents: all params passed to CreateTest{Entity} (excludes self-refs and principal inheritance).
		"nonSelfRefParents": func(parents []ParentFixture) []ParentFixture {
			var result []ParentFixture
			for _, p := range parents {
				if !p.IsSelfReference && !p.IsPrincipalInheritance {
					result = append(result, p)
				}
			}
			return result
		},
		// inBatchParents: parents auto-created inside WithDefaults (in-batch, non-self, non-principal).
		"inBatchParents": func(parents []ParentFixture) []ParentFixture {
			var result []ParentFixture
			for _, p := range parents {
				if !p.IsSelfReference && !p.IsPrincipalInheritance && p.IsInBatch {
					result = append(result, p)
				}
			}
			return result
		},
		// outOfBatchParents: parents outside the batch — become explicit params on WithDefaults.
		"outOfBatchParents": func(parents []ParentFixture) []ParentFixture {
			var result []ParentFixture
			for _, p := range parents {
				if !p.IsSelfReference && !p.IsPrincipalInheritance && !p.IsInBatch {
					result = append(result, p)
				}
			}
			return result
		},
	}

	t, err := template.New("fixture").Funcs(funcMap).Parse(tmplStr)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func positionalArgs(n int) string {
	args := make([]string, n)
	for i := range args {
		args[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(args, ", ")
}

func insertCols(fields []FixtureField) string {
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = f.DBName
	}
	return strings.Join(cols, ", ")
}

func selectCols(fields []FixtureField) string {
	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = f.DBName
	}
	return strings.Join(cols, ", ")
}

// ─── field helpers ──────────────────────────────────────────────────────────

func fieldToFixture(f FieldInfo, allCols []schema.ColumnInfo) FixtureField {
	ff := FixtureField{
		GoName:     f.GoName,
		GoType:     f.GoType,
		DBName:     f.DBName,
		DBType:     f.DBType,
		GoImport:   f.GoImport,
		IsEnum:     f.IsEnum,
		IsNullable: f.IsNullable,
		HasDefault: f.HasDefault,
		MaxLength:  f.MaxLength,
	}

	for _, col := range allCols {
		if col.Name == f.DBName {
			ff.IsNullable = col.IsNullable
			ff.HasDefault = col.HasDefault
			ff.IsForeignKey = col.IsForeignKey
			break
		}
	}

	ff.TestDefault = testDefaultForField(ff)
	return ff
}

func columnToFixture(col schema.ColumnInfo) FixtureField {
	ff := FixtureField{
		GoName:       ToPascalCase(col.Name),
		GoType:       col.GoType,
		DBName:       col.Name,
		DBType:       col.DBType,
		GoImport:     col.GoImport,
		IsEnum:       col.IsEnum,
		IsNullable:   col.IsNullable,
		HasDefault:   col.HasDefault,
		MaxLength:    col.MaxLength,
		IsForeignKey: col.IsForeignKey,
	}
	ff.TestDefault = testDefaultForField(ff)
	return ff
}

func testDefaultForField(f FixtureField) string {
	dbName := strings.ToLower(f.DBName)

	// Handle nullable pointer types.
	if f.IsNullable && strings.HasPrefix(f.GoType, "*") {
		innerType := f.GoType[1:]
		switch innerType {
		case "string":
			return fmt.Sprintf(`conversion.Ptr("test_%s")`, dbName)
		case "bool":
			return "conversion.Ptr(false)"
		case "int", "int32", "int64":
			return "conversion.Ptr(0)"
		case "float64":
			return "conversion.Ptr(0.0)"
		case "time.Time":
			return "conversion.Ptr(time.Now().UTC())"
		case "json.RawMessage":
			// Postgres rejects empty bytes as invalid JSON; emit `{}` so the
			// INSERT succeeds whether the column is jsonb or json.
			return `conversion.Ptr(json.RawMessage("{}"))`
		default:
			return fmt.Sprintf("conversion.Ptr(%s{})", innerType)
		}
	}

	// Non-pointer JSON column.
	if f.GoType == "json.RawMessage" {
		return `json.RawMessage("{}")`
	}

	// Non-pointer types.
	switch f.GoType {
	case "string":
		// Honor varchar(N) length caps so smoke-test inserts don't
		// overflow narrow columns like varchar(12). The unique-ID slice
		// is enough to keep values distinct without prefixing.
		if f.MaxLength > 0 && f.MaxLength < 24 {
			n := f.MaxLength
			if n > 16 {
				n = 16
			}
			if n <= 0 {
				return `""`
			}
			return fmt.Sprintf(`testUniqueID[:%d]`, n)
		}
		switch {
		case dbName == "email" || strings.HasSuffix(dbName, "_email"):
			return `"test_" + testUniqueID[:8] + "@example.com"`
		case strings.Contains(dbName, "record_state"):
			return `"active"`
		case strings.Contains(dbName, "type") || strings.HasSuffix(dbName, "_type"):
			return fmt.Sprintf(`"test_%s"`, dbName)
		case strings.HasSuffix(dbName, "_id"):
			return fmt.Sprintf(`"test_%s_" + testUniqueID[:8]`, dbName)
		default:
			return fmt.Sprintf(`"test_%s_" + testUniqueID[:8]`, dbName)
		}
	case "bool":
		return "false"
	case "int", "int32", "int64":
		return "0"
	case "float64":
		return "0.0"
	case "time.Time":
		return "time.Now().UTC()"
	case "[]byte":
		return `[]byte("test")`
	default:
		return fmt.Sprintf("%s{}", f.GoType)
	}
}

func collectFixtureImports(entities []FixtureEntity) []string {
	seen := map[string]bool{
		"context": true,
		"testing": true,
	}
	var result []string

	for _, e := range entities {
		if !seen[e.RepoImport] {
			seen[e.RepoImport] = true
			result = append(result, e.RepoImport)
		}
		for _, f := range e.InsertFields {
			if f.GoImport != "" && !seen[f.GoImport] {
				seen[f.GoImport] = true
				result = append(result, f.GoImport)
			}
		}
		for _, f := range e.AllColumns {
			if f.GoImport != "" && !seen[f.GoImport] {
				seen[f.GoImport] = true
				result = append(result, f.GoImport)
			}
		}
	}

	sort.Strings(result)
	return result
}
