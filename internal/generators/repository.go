package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// ─── repository template data types ──────────────────────────────────────────

// RepoFieldInfo holds template data for a single struct field.
type RepoFieldInfo struct {
	GoName   string
	GoType   string
	DBName   string
	IsTime   bool
	IsEnum   bool
	Required bool
}

// RepoOrderByField holds data for an OrderBy constant.
type RepoOrderByField struct {
	ConstName string
	DBColumn  string
	GoName    string
	CastLower bool // wrap in LOWER() for case-insensitive text sorting
}

// CursorFieldInfo holds data for cursor value extraction.
type CursorFieldInfo struct {
	GoName string
	DBName string
}

// FilterInfo holds per-function filter data.
type FilterInfo struct {
	FuncName       string
	FilterTypeName string
	GeneratedName  string
	Fields         []RepoFieldInfo
	HasSearchTerm  bool
}

// ResultFieldInfo holds a field in a per-function result struct.
type ResultFieldInfo struct {
	GoName string
	GoType string
	DBName string
}

// MethodSig describes a generated interface method.
type MethodSig struct {
	Name       string
	NameSpaced string // "get user", "soft delete user" — for error wrapping
	Params     string
	Returns    string
	IsListFunc bool
	Category   string
	CallArgs   string

	FilterTypeName string
	PKParams       []string

	ReturnTypeName string
	ReturnFields   []ResultFieldInfo

	// List sub-features (for conditional repo template rendering).
	HasFilters       bool
	HasOrder         bool
	HasLimit         bool
	MaxLimit         int    // from @max annotation; 0 means use fop.DefaultMaxLimit
	ListReturnType   string // Custom return type name (e.g., "ListResourceCreatorsResult").
	ExplicitParams   string // Extra params between filter and orderBy (e.g., "tenantID string").
	ExplicitCallArgs string // Corresponding call args (e.g., "tenantID").

	// Event emission (from @event annotation).
	EventType          string // e.g. "user.created" — empty means no event
	EventOutbox        bool   // true when @event has "outbox" modifier — atomic outbox write instead of bus.Emit
	EventAliasName     string // e.g. "UserCreatedEvent" — for emit call
	EventGeneratedName string // e.g. "GeneratedUserCreatedEvent" — for struct literal
	EventIsDelete      bool   // true when the exec event is a delete/soft-delete
	EventPermanent     bool   // for delete events: true = hard delete, false = soft delete
	EventPKExpr        string // Go variable name for the PK param (e.g. "userID"), resolved by name
}

// RepoEventInfo holds data for generating a single event struct.
type RepoEventInfo struct {
	EventType     string // "user.created"
	GeneratedName string // "GeneratedUserCreatedEvent"
	AliasName     string // "UserCreatedEvent"
	Category      string // "create", "update", "exec"
	IsDelete      bool   // true for delete/soft-delete exec events
	EntityName    string // for create events: "User"
	PKGoName      string // for update/exec events: "UserID"
	PKGoType      string // for update/exec events: "string"
	PKDBName      string // for update/exec events: "user_id"
	UpdateName    string // for update events: "UpdateUser"
}

// RepoTemplateData holds all data needed to render repository templates.
type RepoTemplateData struct {
	PackageName      string
	EntityName       string
	EntityNameLower  string
	EntityNameSpaced string
	EntityNamePlural string
	CreateName       string
	UpdateName       string

	PKColumn          string
	PKGoName          string
	PKGoType          string
	DefaultOrderConst string

	EntityFields  []RepoFieldInfo
	CreateFields  []RepoFieldInfo
	UpdateFields  []RepoFieldInfo
	OrderByFields []RepoOrderByField
	CursorFields  []CursorFieldInfo
	ExtraImports  []string

	Filters []FilterInfo
	Methods []MethodSig

	HasCreate        bool
	HasUpdate        bool
	HasFilter        bool
	HasList          bool
	HasSoftDelete    bool
	HasPKInCreate    bool
	DefaultDirection string

	// Event generation (from @event annotations).
	HasEvents bool
	Events    []RepoEventInfo

	// SkipStorer suppresses Storer interface generation when the developer
	// has defined their own in repository.go.
	SkipStorer bool
}


// GenerateRepository produces flat repository layer files (standard).
// Types use final names, Repository struct + methods are in generated.go,
// no model.go, no GeneratedX prefix.
func GenerateRepository(resolved *ResolvedFile, repoDir string, opts Options) error {
	data, err := buildRepoData(resolved, repoDir)
	if err != nil {
		return fmt.Errorf("repository: %w", err)
	}

	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	genFiles := []genFile{
		{"generated.go", repoGeneratedTemplate, false},
		{"repository.go", repoRepositoryTemplate, true},
		{"fop.go", repoFopTemplate, true},
	}

	for _, f := range genFiles {
		path := filepath.Join(repoDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderRepoTemplate(f.tmpl, data)
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

	// Update Storer interface methods between markers in repository.go.
	repoFile := filepath.Join(repoDir, "repository.go")
	if fileExists(repoFile) && HasMarkers(repoFile) {
		storerMethods := buildStorerMethodsBlock(data)
		if err := ReplaceMarkerBlock(repoFile, storerMethods); err != nil {
			return fmt.Errorf("update Storer in repository.go: %w", err)
		}
		fmt.Printf("      update Storer interface in %s\n", repoFile)
	}

	return nil
}

// buildStorerMethodsBlock generates the Storer interface method lines for marker replacement.
func buildStorerMethodsBlock(data RepoTemplateData) string {
	var b strings.Builder
	for _, m := range data.Methods {
		params := m.Params
		if m.EventOutbox {
			params += ", outboxEvents ...events.OutboxEvent"
		}
		fmt.Fprintf(&b, "\t%s(%s) %s\n", m.Name, params, m.Returns)
	}
	return b.String()
}

// buildRepoData builds template data for generation, filtering out
// methods that the developer has already customized in repository.go.
func buildRepoData(resolved *ResolvedFile, repoDir string) (RepoTemplateData, error) {
	data, err := buildRepoTemplateData(resolved)
	if err != nil {
		return data, err
	}

	// Check repository.go for custom methods that should skip generation.
	repoFile := filepath.Join(repoDir, "repository.go")
	if fileExists(repoFile) {
		var kept []MethodSig
		for _, m := range data.Methods {
			exists, err := MethodExistsOnType(repoFile, "Repository", m.Name)
			if err != nil {
				return data, fmt.Errorf("check custom method %s: %w", m.Name, err)
			}
			if exists {
				fmt.Printf("      skip method %s (customized in repository.go)\n", m.Name)
				continue
			}
			kept = append(kept, m)
		}
		data.Methods = kept
	}

	return data, nil
}

var repoFuncs = template.FuncMap{
	"not": func(v bool) bool { return !v },
}

func renderRepoTemplate(tmplText string, data RepoTemplateData) ([]byte, error) {
	t, err := template.New("").Funcs(repoFuncs).Parse(tmplText)
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

func buildRepoTemplateData(resolved *ResolvedFile) (RepoTemplateData, error) {
	importSet := map[string]bool{}

	var entityFields []RepoFieldInfo
	textColumns := map[string]bool{}
	var cursorFields []CursorFieldInfo
	hasSoftDelete := false

	for _, col := range resolved.AllColumns {
		if col.Name == "record_state" {
			hasSoftDelete = true
		}
		goName := ToPascalCase(col.Name)
		if col.GoImport != "" {
			importSet[col.GoImport] = true
		}
		entityFields = append(entityFields, RepoFieldInfo{
			GoName: goName,
			GoType: col.GoType,
			DBName: col.Name,
			IsTime: col.GoImport == "time",
			IsEnum: col.IsEnum,
		})

		goType := strings.TrimPrefix(col.GoType, "*")
		if goType == "string" && !col.IsEnum {
			textColumns[col.Name] = true
		}

		cursorFields = append(cursorFields, CursorFieldInfo{
			GoName: goName,
			DBName: col.Name,
		})
	}

	var createFields, updateFields []RepoFieldInfo
	var orderByFields []RepoOrderByField
	hasCreate, hasUpdate, hasFilter, hasList := false, false, false, false

	createSeen := map[string]bool{}
	updateSeen := map[string]bool{}
	orderSeen := map[string]bool{}

	var filters []FilterInfo

	for _, rq := range resolved.Queries {
		for _, f := range rq.InsertFields {
			if !createSeen[f.DBName] {
				createSeen[f.DBName] = true
				createFields = append(createFields, toRepoCreateFieldInfo(f))
				hasCreate = true
				if f.GoImport != "" {
					importSet[f.GoImport] = true
				}
			}
		}
		for _, f := range rq.SetFields {
			if !updateSeen[f.DBName] {
				updateSeen[f.DBName] = true
				updateFields = append(updateFields, toRepoFieldInfo(f))
				hasUpdate = true
				if f.GoImport != "" {
					importSet[f.GoImport] = true
				}
			}
		}
		if rq.HasFilters || rq.HasOrder || rq.HasLimit {
			hasList = true
		}
		if rq.HasFilters {
			hasFilter = true

			var filterFields []RepoFieldInfo
			for _, f := range rq.AllFilterFields() {
				filterFields = append(filterFields, toRepoFieldInfo(f))
				if f.GoImport != "" {
					importSet[f.GoImport] = true
				}
			}

			filters = append(filters, FilterInfo{
				FuncName:       rq.FuncName,
				FilterTypeName: "Filter" + rq.FuncName,
				GeneratedName:  "GeneratedFilter" + rq.FuncName,
				Fields:         filterFields,
				HasSearchTerm:  len(rq.SearchFields) > 0,
			})
		}
		for _, o := range rq.OrderFields {
			// Use GoName as dedup key — handles qualified ("u.email") and unqualified ("email") as same.
			if o.GoName == ToPascalCase(resolved.PKColumn) {
				continue
			}
			if !orderSeen[o.GoName] {
				orderSeen[o.GoName] = true
				orderByFields = append(orderByFields, RepoOrderByField{
					ConstName: o.ConstName,
					DBColumn:  o.DBColumn,
					GoName:    o.GoName,
					CastLower: textColumns[o.DBColumn],
				})
			}
		}
	}

	hasPKInCreate := resolved.PKGoType == "string" && createSeen[resolved.PKColumn]

	defaultOrderConst := "OrderByPK"
	for _, ob := range orderByFields {
		if ob.GoName == "CreatedAt" {
			defaultOrderConst = ob.ConstName
			break
		}
	}

	defaultDirection := "fop.ASC"
	if defaultOrderConst == "OrderByCreatedAt" {
		defaultDirection = "fop.DESC"
	}

	methods, err := buildRepoMethods(resolved)
	if err != nil {
		return RepoTemplateData{}, err
	}

	var extraImports []string
	for imp := range importSet {
		if imp == "time" {
			continue
		}
		extraImports = append(extraImports, imp)
	}
	sort.Strings(extraImports)

	// Collect unique events from methods.
	var events []RepoEventInfo
	eventSeen := map[string]bool{}
	for _, m := range methods {
		if m.EventType == "" || eventSeen[m.EventType] {
			continue
		}
		eventSeen[m.EventType] = true

		alias := eventAliasName(m.EventType)
		ei := RepoEventInfo{
			EventType:     m.EventType,
			GeneratedName: "Generated" + alias,
			AliasName:     alias,
			EntityName:    resolved.EntityName,
			PKGoName:      resolved.PKGoName,
			PKGoType:      resolved.PKGoType,
			PKDBName:      resolved.PKColumn,
			UpdateName:    "Update" + resolved.EntityName,
		}

		// Determine category from the first method that uses this event type.
		switch m.Category {
		case "create":
			ei.Category = "create"
		case "update", "update_returning":
			ei.Category = "update"
		default:
			ei.Category = "exec"
			lower := strings.ToLower(m.Name)
			ei.IsDelete = strings.Contains(lower, "delete") || strings.Contains(lower, "softdelete")
		}

		events = append(events, ei)
	}

	return RepoTemplateData{
		PackageName:       resolved.PackageName,
		EntityName:        resolved.EntityName,
		EntityNameLower:   resolved.EntityLower,
		EntityNameSpaced:  ToSpaced(strings.ToLower(resolved.EntityName)),
		EntityNamePlural:  resolved.EntityPlural,
		CreateName:        "Create" + resolved.EntityName,
		UpdateName:        "Update" + resolved.EntityName,
		PKColumn:          resolved.PKColumn,
		PKGoName:          resolved.PKGoName,
		PKGoType:          resolved.PKGoType,
		DefaultOrderConst: defaultOrderConst,
		EntityFields:      entityFields,
		CreateFields:      createFields,
		UpdateFields:      updateFields,
		OrderByFields:     orderByFields,
		CursorFields:      cursorFields,
		ExtraImports:      extraImports,
		Filters:           filters,
		Methods:           methods,
		HasCreate:         hasCreate,
		HasUpdate:         hasUpdate,
		HasFilter:         hasFilter,
		HasList:           hasList,
		HasSoftDelete:     hasSoftDelete,
		HasPKInCreate:     hasPKInCreate,
		DefaultDirection:  defaultDirection,
		HasEvents:         true,
		Events:            events,
	}, nil
}

func toRepoFieldInfo(f FieldInfo) RepoFieldInfo {
	goType := strings.TrimPrefix(f.GoType, "*")
	return RepoFieldInfo{
		GoName: f.GoName,
		GoType: goType,
		DBName: f.DBName,
		IsTime: f.IsTime,
		IsEnum: f.IsEnum,
	}
}

// toRepoCreateFieldInfo preserves the pointer type for nullable columns
// so that the CreateInput struct correctly represents optionality.
func toRepoCreateFieldInfo(f FieldInfo) RepoFieldInfo {
	return RepoFieldInfo{
		GoName: f.GoName,
		GoType: f.GoType, // preserve *string, *time.Time etc.
		DBName: f.DBName,
		IsTime: f.IsTime,
		IsEnum: f.IsEnum,
	}
}

func buildRepoMethods(resolved *ResolvedFile) ([]MethodSig, error) {
	var methods []MethodSig
	for _, rq := range resolved.Queries {
		m := MethodSig{
			Name:       rq.FuncName,
			NameSpaced: PascalToSpaced(rq.FuncName),
		}

		params := []string{"ctx context.Context"}
		var callArgs []string

		switch {
		case rq.ScanOverride == "many":
			m.Category = "scan_many"
			callArgs = []string{"ctx"}
			for _, p := range rq.Params {
				goType := "string"
				if t, ok := rq.ParamTypes[p]; ok {
					goType = t
				}
				goName := ToCamelCase(p)
				params = append(params, goName+" "+goType)
				callArgs = append(callArgs, goName)
				m.PKParams = append(m.PKParams, goName)
			}

		case rq.HasFilters || rq.HasOrder || rq.HasLimit:
			m.IsListFunc = true
			m.Category = "list"
			m.HasFilters = rq.HasFilters
			m.HasOrder = rq.HasOrder
			m.HasLimit = rq.HasLimit
			m.MaxLimit = rq.MaxLimit
			callArgs = []string{"ctx"}

			if rq.HasFilters {
				m.FilterTypeName = "Filter" + rq.FuncName
				params = append(params, "filter Filter"+rq.FuncName)
				callArgs = append(callArgs, "filter")
			}

			// Add explicit params (e.g., @tenant_id) for all lists.
			var explicitParamParts []string
			var explicitCallParts []string
			for _, p := range rq.Params {
				goType := "string"
				if t, ok := rq.ParamTypes[p]; ok {
					goType = t
				}
				goName := ToCamelCase(p)
				params = append(params, goName+" "+goType)
				callArgs = append(callArgs, goName)
				explicitParamParts = append(explicitParamParts, goName+" "+goType)
				explicitCallParts = append(explicitCallParts, goName)
			}
			if len(explicitParamParts) > 0 {
				m.ExplicitParams = strings.Join(explicitParamParts, ", ")
				m.ExplicitCallArgs = strings.Join(explicitCallParts, ", ")
			}

			if rq.HasOrder {
				params = append(params,
					"orderBy fop.Order",
					"page fop.PageStringCursor",
					"forPrevious bool",
				)
				callArgs = append(callArgs, "orderBy", "page", "forPrevious")
			} else if rq.HasLimit {
				params = append(params, "limit int")
				callArgs = append(callArgs, "limit")
			}

			// Custom return type for lists with explicit SELECT containing non-entity columns.
			if len(rq.ReturnFields) > 0 && hasNonEntityColumns(rq.ReturnFields, resolved.AllColumns) {
				m.ReturnTypeName = rq.FuncName + "Result"
				m.ListReturnType = rq.FuncName + "Result"
				for _, f := range rq.ReturnFields {
					m.ReturnFields = append(m.ReturnFields, ResultFieldInfo{
						GoName: f.GoName,
						GoType: strings.TrimPrefix(f.GoType, "*"),
						DBName: f.DBName,
					})
				}
			}

		case rq.HasFields && rq.Type == QueryInsert:
			m.Category = "create"
			params = []string{"ctx context.Context", "input Create" + resolved.EntityName}
			callArgs = []string{"ctx", "input"}

		case rq.HasFields && rq.Type == QueryUpdate && rq.ReturnsRows:
			m.Category = "update_returning"
			params = []string{"ctx context.Context"}
			callArgs = []string{"ctx"}
			for _, p := range rq.Params {
				goType := "string"
				if t, ok := rq.ParamTypes[p]; ok {
					goType = t
				}
				goName := ToCamelCase(p)
				params = append(params, goName+" "+goType)
				callArgs = append(callArgs, goName)
				m.PKParams = append(m.PKParams, goName)
			}
			params = append(params, "input Update"+resolved.EntityName)
			callArgs = append(callArgs, "input")

		case rq.HasFields && rq.Type == QueryUpdate:
			m.Category = "update"
			params = []string{"ctx context.Context"}
			callArgs = []string{"ctx"}
			for _, p := range rq.Params {
				goType := "string"
				if t, ok := rq.ParamTypes[p]; ok {
					goType = t
				}
				goName := ToCamelCase(p)
				params = append(params, goName+" "+goType)
				callArgs = append(callArgs, goName)
				m.PKParams = append(m.PKParams, goName)
			}
			params = append(params, "input Update"+resolved.EntityName)
			callArgs = append(callArgs, "input")

		case rq.ReturnsRows && len(rq.ReturnFields) > 0:
			m.Category = "scan_one_custom"
			m.ReturnTypeName = rq.FuncName + "Result"
			for _, f := range rq.ReturnFields {
				m.ReturnFields = append(m.ReturnFields, ResultFieldInfo{
					GoName: f.GoName,
					GoType: strings.TrimPrefix(f.GoType, "*"),
					DBName: f.DBName,
				})
			}
			callArgs = []string{"ctx"}
			for _, p := range rq.Params {
				goType := "string"
				if t, ok := rq.ParamTypes[p]; ok {
					goType = t
				}
				goName := ToCamelCase(p)
				params = append(params, goName+" "+goType)
				callArgs = append(callArgs, goName)
				m.PKParams = append(m.PKParams, goName)
			}

		case rq.ReturnsRows:
			m.Category = "scan_one"
			callArgs = []string{"ctx"}
			for _, p := range rq.Params {
				goType := "string"
				if t, ok := rq.ParamTypes[p]; ok {
					goType = t
				}
				goName := ToCamelCase(p)
				params = append(params, goName+" "+goType)
				callArgs = append(callArgs, goName)
				m.PKParams = append(m.PKParams, goName)
			}

		default:
			m.Category = "exec"
			callArgs = []string{"ctx"}
			for _, p := range rq.Params {
				goType := "string"
				if t, ok := rq.ParamTypes[p]; ok {
					goType = t
				}
				goName := ToCamelCase(p)
				params = append(params, goName+" "+goType)
				callArgs = append(callArgs, goName)
				m.PKParams = append(m.PKParams, goName)
			}
		}

		m.Params = strings.Join(params, ", ")
		if len(callArgs) > 0 {
			m.CallArgs = strings.Join(callArgs, ", ")
		}

		switch m.Category {
		case "list":
			if m.ListReturnType != "" {
				m.Returns = "([]" + m.ListReturnType + ", error)"
			} else {
				m.Returns = "([]" + resolved.EntityName + ", error)"
			}
		case "scan_many":
			m.Returns = "([]" + resolved.EntityName + ", error)"
		case "create", "scan_one", "update_returning":
			m.Returns = "(" + resolved.EntityName + ", error)"
		case "scan_one_custom":
			m.Returns = "(" + m.ReturnTypeName + ", error)"
		default:
			m.Returns = "error"
		}

		// Event annotation.
		if rq.EventType != "" {
			m.EventType = rq.EventType
			m.EventOutbox = rq.EventOutbox
			m.EventAliasName = eventAliasName(rq.EventType)
			m.EventGeneratedName = "Generated" + m.EventAliasName
			// For delete exec events, determine Permanent flag from func name.
			if m.Category == "exec" {
				lower := strings.ToLower(rq.FuncName)
				m.EventIsDelete = strings.Contains(lower, "delete") || strings.Contains(lower, "softdelete")
				if m.EventIsDelete {
					m.EventPermanent = !strings.Contains(lower, "soft")
				}
			}
			// Resolve the PK param by name for event emission.
			// Create events don't need this — the PK comes from the returned record.
			if m.Category != "create" {
				pkExpr := FindPKParam(m.PKParams, resolved.PKColumn)
				if pkExpr == "" {
					return nil, fmt.Errorf("event %q on function %q: no parameter matching primary key %q", rq.EventType, rq.FuncName, resolved.PKColumn)
				}
				m.EventPKExpr = pkExpr
			}
		}

		methods = append(methods, m)
	}
	return methods, nil
}

// eventAliasName derives a Go type name from an event type string.
// "user.created" → "UserCreatedEvent"
func eventAliasName(eventType string) string {
	parts := strings.Split(eventType, ".")
	var result string
	for _, p := range parts {
		result += ToPascalCase(p)
	}
	return result + "Event"
}
