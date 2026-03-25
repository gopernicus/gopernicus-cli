package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
	"text/template"
)

// GenerateBridge generates flat HTTP bridge files from bridge.yml.
// Bridge struct has all fields directly, handlers on *Bridge, no GeneratedX.
// Routes are wired via addGeneratedRoutes() in generated.go, called from routes.go.
// Returns (false, nil) if no bridge.yml exists.
func GenerateBridge(resolved *ResolvedFile, domainName, modulePath, projectRoot string, authEnabled bool, opts Options) (bool, error) {
	bridgeDir := BridgeDir(domainName, resolved.TableName, projectRoot)
	ymlPath := filepath.Join(bridgeDir, "bridge.yml")

	if !fileExists(ymlPath) {
		return false, nil
	}

	yml, err := ParseBridgeYML(ymlPath)
	if err != nil {
		return false, err
	}

	if len(yml.Routes) == 0 {
		return false, nil
	}

	data, err := buildBridgeData(yml, resolved, domainName, modulePath, authEnabled, bridgeDir)
	if err != nil {
		return false, fmt.Errorf("bridge: %w", err)
	}

	entitySingular := Singularize(resolved.TableName)
	data.EntitySingular = entitySingular

	if err := ensureDir(bridgeDir, opts); err != nil {
		return false, fmt.Errorf("create bridge directory: %w", err)
	}

	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	genFiles := []genFile{
		{"generated.go", bridgeGeneratedTemplate, false},
		{"bridge.go", bridgeBridgeTemplate, true},
		{"routes.go", bridgeRoutesTemplate, true},
		{"http.go", bridgeHttpTemplate, true},
		{"fop.go", bridgeFopTemplate, true},
	}

	for _, f := range genFiles {
		path := filepath.Join(bridgeDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderBridgeTemplate(f.tmpl, data)
		if err != nil {
			return false, fmt.Errorf("render %s for %s: %w", f.name, resolved.TableName, err)
		}

		formatted, err := format.Source(out)
		if err != nil {
			_ = writeFile(path, out, opts)
			return false, fmt.Errorf("go/format %s: %w\nUnformatted output written for debugging.", f.name, err)
		}

		if err := writeFile(path, formatted, opts); err != nil {
			return false, fmt.Errorf("write %s: %w", f.name, err)
		}

		verb := "write"
		if f.bootstrap {
			verb = "create"
		}
		fmt.Printf("      %s %s\n", verb, path)
	}

	return true, nil
}

// buildBridgeData constructs BridgeTemplateData from bridge.yml + resolved queries.
func buildBridgeData(yml *BridgeYML, resolved *ResolvedFile, domainName, modulePath string, authEnabled bool, bridgeDir string) (BridgeTemplateData, error) {
	data := BridgeTemplateData{
		BridgePackage:    BridgePackage(resolved.TableName),
		RepoPackage:      RepoPackage(resolved.TableName),
		ModulePath:       modulePath,
		FrameworkPath:    goperniculusFrameworkPath,
		Module:           domainName,
		EntityName:       resolved.EntityName,
		EntityNameLower:  ToCamelCase(Singularize(resolved.TableName)),
		EntityNamePlural: resolved.EntityPlural,
		PKColumn:         resolved.PKColumn,
		PKGoName:         resolved.PKGoName,
		PKGoType:         resolved.PKGoType,
		PKURLParam:       resolved.PKColumn,
		AuthEnabled:      authEnabled,
		NeedsFmtImport:   true,
	}

	// Convert bridge.yml routes to BridgeRoutes.
	routes, err := BridgeYMLToBridgeRoutes(yml, resolved)
	if err != nil {
		return BridgeTemplateData{}, err
	}

	// All routes from bridge.yml are generated. To customize a handler,
	// remove the route from bridge.yml and write your own in routes.go.
	for _, br := range routes {
		data.Routes = append(data.Routes, br)

		// Aggregate create rels.
		if len(br.CreateRels) > 0 {
			data.AllCreateRels = append(data.AllCreateRels, br.CreateRels...)
			data.HasCreateRels = true
		}

		// Flag delete cleanup.
		if br.DeleteCleanup && authEnabled {
			data.HasDeleteRels = true
		}
	}

	// Build a set of params_to_input field names — these come from the URL path,
	// not the request body. Exclude them from bridge request types.
	paramsToInputFields := make(map[string]bool)
	for _, route := range data.Routes {
		for _, p := range route.ParamsToInput {
			paramsToInputFields[p.Name] = true
		}
	}

	// Collect per-query field data (filters, create/update fields) for routes.
	seenList := make(map[string]bool)
	seenCreate := make(map[string]bool)
	seenUpdate := make(map[string]bool)

	for _, route := range data.Routes {
		rq, ok := findResolvedQuery(resolved, route.FuncName)
		if !ok {
			continue
		}
		collectPerQueryData(&data, rq, seenList, seenCreate, seenUpdate, paramsToInputFields)
	}

	// Set import flags for auth features.
	if data.AuthEnabled {
		data.NeedsAuthorizationImport = true
	}
	if data.HasCreateRels {
		data.NeedsStringsImport = true
		data.NeedsContextImport = true
		data.NeedsHTTPMidImport = true
	}
	if data.HasDeleteRels {
		data.NeedsContextImport = true
	}

	// Set import flags for authorize annotations and collect postfilter routes.
	seenPostfilter := make(map[string]bool)
	for _, route := range data.Routes {
		if route.Authorize != nil {
			data.NeedsAuthorizationImport = true
			data.NeedsHTTPMidImport = true
		}
		if route.WithPermissions {
			data.NeedsAuthorizationImport = true
			data.NeedsHTTPMidImport = true
		}
		switch route.Category {
		case "scan_one", "list", "create", "update_returning":
			data.NeedsBridgeFOPImport = true
		}
		if route.Category == "list" && route.Authorize != nil && route.Authorize.Pattern == "postfilter" {
			if !seenPostfilter[route.FuncName] {
				seenPostfilter[route.FuncName] = true
				data.PostfilterRoutes = append(data.PostfilterRoutes, route)
				data.HasPostfilterRoutes = true
			}
		}
	}
	if data.HasPostfilterRoutes {
		data.NeedsContextImport = true
		data.NeedsBridgeFOPImport = true
	}

	// Check if any route uses unique_to_id middleware (needs context import for closure).
	for _, route := range data.Routes {
		for _, mw := range route.MiddlewareChain {
			if mw.UniqueToID != nil {
				data.NeedsContextImport = true
				break
			}
		}
	}

	return data, nil
}

var bridgeFuncs = template.FuncMap{
	"lower":    strings.ToLower,
	"camel":    ToCamelCase,
	"contains": strings.Contains,
	"enumArgs": func(vals []string) string {
		quoted := make([]string, len(vals))
		for i, v := range vals {
			quoted[i] = fmt.Sprintf("%q", v)
		}
		return strings.Join(quoted, ", ")
	},
	"specSummary": func(funcName, entityName, entityPlural string) string {
		lower := strings.ToLower(funcName)
		switch {
		case lower == "list" || strings.HasPrefix(lower, "listby"):
			return "List " + strings.ToLower(entityPlural)
		case lower == "get" || strings.HasPrefix(lower, "getby"):
			return "Get " + strings.ToLower(entityName)
		case lower == "create":
			return "Create " + strings.ToLower(entityName)
		case lower == "update":
			return "Update " + strings.ToLower(entityName)
		case lower == "delete" || lower == "harddelete":
			return "Delete " + strings.ToLower(entityName)
		default:
			return funcName + " " + strings.ToLower(entityName)
		}
	},
	"isPaginated": func(category string) bool {
		return category == "list"
	},
	"isAuthenticated": func(authenticated string) bool {
		return authenticated != ""
	},
	"hasValidation": func(fields []BridgeField) bool {
		for _, f := range fields {
			if f.IsRequired || f.MaxLength > 0 || f.IsEnum || f.IsEmail || f.IsURL || f.IsSlug || f.IsUUID {
				return true
			}
		}
		return false
	},
	// subjectType extracts the type part of a subject reference.
	// "tenant:tenant_id" → "tenant"
	"subjectType": func(ref string) string {
		parts := strings.SplitN(ref, ":", 2)
		return parts[0]
	},
	// subjectIDExpr generates a Go expression for the subject ID from a subject reference.
	// "tenant:tenant_id" → tenantID (the local variable from path param extraction)
	"subjectIDExpr": func(ref string) string {
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) == 2 {
			return ToCamelCase(parts[1])
		}
		return ref
	},
}

func renderBridgeTemplate(tmplText string, data BridgeTemplateData) ([]byte, error) {
	t, err := template.New("").Funcs(bridgeFuncs).Parse(tmplText)
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

// categorizeQuery determines the bridge handler category for a query.
func categorizeQuery(rq ResolvedQuery) string {
	switch {
	case rq.HasFilters || rq.HasOrder || rq.HasLimit:
		return "list"
	case rq.HasFields && rq.Type == QueryInsert:
		return "create"
	case rq.HasFields && rq.Type == QueryUpdate && rq.ReturnsRows:
		return "update_returning"
	case rq.HasFields && rq.Type == QueryUpdate:
		return "update"
	case rq.ReturnsRows:
		return "scan_one"
	default:
		return "exec"
	}
}

// extractPathParams extracts {param_name} segments from a route path.
func extractPathParams(path string, rq ResolvedQuery) []PathParam {
	var params []PathParam
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			name := segment[1 : len(segment)-1]
			goType := "string"
			if t, ok := rq.ParamTypes[name]; ok {
				goType = t
			}
			params = append(params, PathParam{
				Name:        name,
				GoName:      ToCamelCase(name),
				GoFieldName: ToPascalCase(name),
				GoType:      goType,
			})
		}
	}
	return params
}

// collectPerQueryData collects per-query list/create/update data, excluding parent path
// params from create request fields (they come from the URL, not the body).
func collectPerQueryData(data *BridgeTemplateData, rq ResolvedQuery, seenList, seenCreate, seenUpdate map[string]bool, createPathParams map[string]bool) {
	category := categorizeQuery(rq)

	switch category {
	case "list":
		if seenList[rq.FuncName] {
			return
		}
		seenList[rq.FuncName] = true

		lq := BridgeListQuery{
			FuncName:  rq.FuncName,
			HasSearch: rq.HasSearch,
		}
		for _, f := range rq.AllFilterFields() {
			bf := toBridgeField(f)
			lq.FilterFields = append(lq.FilterFields, bf)
			updateImportFlagsNoValidation(data, bf)
		}
		data.ListQueries = append(data.ListQueries, lq)

	case "create":
		if seenCreate[rq.FuncName] {
			return
		}
		seenCreate[rq.FuncName] = true

		cq := BridgeCreateQuery{FuncName: rq.FuncName}
		for _, f := range rq.InsertFields {
			// Skip parent FK fields — they come from URL path params, not request body.
			if createPathParams[f.DBName] {
				continue
			}
			bf := toBridgeField(f)
			cq.Fields = append(cq.Fields, bf)
			updateImportFlags(data, bf)
		}
		data.CreateQueries = append(data.CreateQueries, cq)

	case "update", "update_returning":
		if seenUpdate[rq.FuncName] {
			return
		}
		seenUpdate[rq.FuncName] = true

		uq := BridgeUpdateQuery{FuncName: rq.FuncName}
		for _, f := range rq.SetFields {
			bf := toBridgeField(f)
			bf.IsPointer = true
			if strings.HasPrefix(bf.GoType, "*") {
				bf.UpdateGoType = bf.GoType
			} else {
				bf.UpdateGoType = "*" + bf.GoType
			}
			uq.Fields = append(uq.Fields, bf)
			updateImportFlags(data, bf)
		}
		data.UpdateQueries = append(data.UpdateQueries, uq)
	}
}

func toBridgeField(f FieldInfo) BridgeField {
	goType := f.GoType
	isPointer := strings.HasPrefix(goType, "*")
	isString := strings.TrimPrefix(goType, "*") == "string"
	// A field is required for create if: NOT NULL, no default, not PK, not FK.
	isRequired := !f.IsNullable && !f.HasDefault && !f.IsPrimaryKey && !f.IsForeignKey && !isPointer

	lower := strings.ToLower(f.DBName)

	return BridgeField{
		GoName:     f.GoName,
		GoType:     goType,
		DBName:     f.DBName,
		IsTime:     f.IsTime,
		IsString:   isString,
		IsPointer:  isPointer,
		IsRequired: isRequired,
		MaxLength:  f.MaxLength,
		IsEnum:     f.IsEnum,
		EnumValues: f.EnumValues,
		IsEmail:    isString && (lower == "email" || strings.HasSuffix(lower, "_email")),
		IsURL:      isString && (lower == "url" || strings.HasSuffix(lower, "_url") || lower == "website" || lower == "homepage"),
		IsSlug:     isString && (lower == "slug" || strings.HasSuffix(lower, "_slug")),
		IsUUID:     isString && strings.HasPrefix(strings.ToLower(f.DBType), "uuid"),
	}
}

// resolveBridgeCreateRels converts parsed AuthCreateRel placeholders into
// Go expressions suitable for template rendering.
//
// Placeholder resolution:
//
//	{=subject} or {subject} → from authenticated context (SubjectFromContext=true)
//	{field_name}            → record.GoFieldName
//	literal                 → quoted string literal
func resolveBridgeCreateRels(rels []AuthCreateRel) []BridgeCreateRel {
	result := make([]BridgeCreateRel, len(rels))
	for i, rel := range rels {
		br := BridgeCreateRel{
			ResourceType: rel.ResourceType,
			Relation:     rel.Relation,
		}

		// Resolve resource ID.
		br.ResourceIDExpr = resolveRelPlaceholder(rel.ResourceID)

		// Resolve subject.
		if isContextPlaceholder(rel.SubjectType) {
			br.SubjectFromContext = true
		} else if isPlaceholder(rel.SubjectType) {
			// {some_type} — unusual but handle it.
			br.SubjectType = placeholderInner(rel.SubjectType)
		} else {
			br.SubjectType = rel.SubjectType
		}

		if !br.SubjectFromContext && rel.SubjectID != "" {
			br.SubjectIDExpr = resolveRelPlaceholder(rel.SubjectID)
		}

		result[i] = br
	}
	return result
}

func resolveRelPlaceholder(s string) string {
	if isPlaceholder(s) {
		name := placeholderInner(s)
		return "record." + ToPascalCase(name)
	}
	return `"` + s + `"`
}

func isPlaceholder(s string) bool {
	return len(s) >= 2 && s[0] == '{' && s[len(s)-1] == '}'
}

func placeholderInner(s string) string {
	return s[1 : len(s)-1]
}

// isContextPlaceholder returns true for {subject}, {=subject}, and other
// context-derived placeholders (prefixed with =).
func isContextPlaceholder(s string) bool {
	if !isPlaceholder(s) {
		return false
	}
	name := placeholderInner(s)
	return name == "subject" || strings.HasPrefix(name, "=")
}

// isDeleteFunc returns true if the function name indicates a hard delete
// (not soft-delete or archive). Only hard deletes need auth relationship cleanup.
func isDeleteFunc(funcName string) bool {
	lower := strings.ToLower(funcName)
	return lower == "delete" || lower == "harddelete"
}

// updateImportFlagsNoValidation updates import flags but excludes validation,
// since list filter fields don't use the validation package at runtime.
func updateImportFlagsNoValidation(data *BridgeTemplateData, bf BridgeField) {
	if bf.IsTime {
		data.NeedsTimeImport = true
	}
	baseType := strings.TrimPrefix(bf.GoType, "*")
	if baseType == "bool" || baseType == "int" || baseType == "int64" {
		data.NeedsStrconvImport = true
	}
	if strings.Contains(bf.GoType, "json.") {
		data.NeedsJSONImport = true
	}
}

func updateImportFlags(data *BridgeTemplateData, bf BridgeField) {
	if bf.IsTime {
		data.NeedsTimeImport = true
	}
	baseType := strings.TrimPrefix(bf.GoType, "*")
	if baseType == "bool" || baseType == "int" || baseType == "int64" {
		data.NeedsStrconvImport = true
	}
	if strings.Contains(bf.GoType, "json.") {
		data.NeedsJSONImport = true
	}
	if bf.IsEnum || bf.IsRequired || bf.MaxLength > 0 || bf.IsEmail || bf.IsURL || bf.IsSlug || bf.IsUUID {
		data.NeedsValidationImport = true
	}
}
