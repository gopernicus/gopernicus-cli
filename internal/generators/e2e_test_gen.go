package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
	"text/template"
)

// ─── E2E test template data types ───────────────────────────────────────────

// E2ETestField describes a field for building test request payloads.
type E2ETestField struct {
	JSONName    string // e.g. "email"
	TestDefault string // Go expression for test value
	IsFK        bool   // skip in payload, use fixture
}

// E2ETestData holds all data for rendering an E2E test file.
type E2ETestData struct {
	EntityName  string // e.g. "User"
	EntityLower string // e.g. "user"
	RepoPkg     string // e.g. "users"
	DomainName  string // e.g. "auth"

	PKColumn string // e.g. "user_id"
	PKGoName string // e.g. "UserID"

	// Import paths
	ModulePath    string
	FrameworkPath string // gopernicus framework module path (for infra imports)
	FixtureImport string

	// Route paths (empty string = route doesn't exist)
	CreatePath  string // POST /users
	GetPath     string // GET /users/:user_id
	ListPath    string // GET /users
	UpdatePath  string // PUT /users/:user_id
	DeletePath  string // DELETE /users/:user_id

	// State change paths
	SoftDeletePath string // PUT /users/:user_id/delete
	ArchivePath    string // PUT /users/:user_id/archive
	RestorePath    string // PUT /users/:user_id/restore

	// Feature flags
	HasCreate     bool
	HasGet        bool
	HasList       bool
	HasUpdate     bool
	HasDelete     bool
	HasSoftDelete bool
	HasArchive    bool
	HasRestore    bool

	// Fields for building Create request payload.
	CreateFields []E2ETestField

	// A representative field name for Update test (first non-PK, non-FK string field).
	UpdateFieldJSON    string // e.g. "display_name"
	UpdateFieldTestVal string // e.g. `"updated_value"`
	HasUpdateField     bool
}

// BuildE2ETestData creates E2E test data from a resolved file.
func BuildE2ETestData(resolved *ResolvedFile, modulePath string) (*E2ETestData, error) {
	data := &E2ETestData{
		EntityName:    resolved.EntityName,
		EntityLower:   resolved.EntityLower,
		RepoPkg:       resolved.PackageName,
		DomainName:    resolved.DomainName,
		PKColumn:      resolved.PKColumn,
		PKGoName:      resolved.PKGoName,
		ModulePath:    modulePath,
		FrameworkPath: goperniculusFrameworkPath,
		FixtureImport: modulePath + "/core/testing/fixtures",
	}

	methods, err := buildRepoMethods(resolved)
	if err != nil {
		return nil, err
	}

	// E2E route discovery now relies on bridge.yml, not annotation-based routes.
	// This function is only called when resolvedHasBridgeRoutes returns true,
	// which no longer happens since protocol config moved to bridge.yml.
	_ = methods

	// Build Create payload fields from first INSERT query.
	for _, rq := range resolved.Queries {
		if rq.Type == QueryInsert && len(rq.InsertFields) > 0 {
			for _, f := range rq.InsertFields {
				if f.DBName == "created_at" || f.DBName == "updated_at" {
					continue
				}
				ef := E2ETestField{
					JSONName:    f.DBName,
					IsFK:        f.IsForeignKey,
					TestDefault: testDefaultForE2EField(f),
				}
				data.CreateFields = append(data.CreateFields, ef)
			}
			break
		}
	}

	// Find a representative update field (first non-PK, non-FK nullable string).
	for _, rq := range resolved.Queries {
		if m := methods[indexOf(resolved.Queries, rq)]; m.Name == "Update" {
			for _, f := range rq.SetFields {
				if f.DBName == resolved.PKColumn || f.IsForeignKey {
					continue
				}
				if f.DBName == "updated_at" || f.DBName == "created_at" {
					continue
				}
				if strings.HasPrefix(f.GoType, "*string") || f.GoType == "*string" {
					data.HasUpdateField = true
					data.UpdateFieldJSON = f.DBName
					data.UpdateFieldTestVal = `"updated_test_value"`
					break
				}
				if f.GoType == "string" {
					data.HasUpdateField = true
					data.UpdateFieldJSON = f.DBName
					data.UpdateFieldTestVal = `"updated_test_value"`
					break
				}
			}
			break
		}
	}

	return data, nil
}

func indexOf(queries []ResolvedQuery, target ResolvedQuery) int {
	for i, q := range queries {
		if q.FuncName == target.FuncName {
			return i
		}
	}
	return 0
}

func testDefaultForE2EField(f FieldInfo) string {
	dbName := strings.ToLower(f.DBName)

	if f.IsNullable && strings.HasPrefix(f.GoType, "*") {
		// Skip nullable fields in create payload — let them be null.
		return ""
	}

	switch f.GoType {
	case "string":
		switch {
		case dbName == "email" || strings.HasSuffix(dbName, "_email"):
			return `"e2e_test_" + testUniqueID + "@example.com"`
		case strings.Contains(dbName, "record_state"):
			return `"active"`
		case strings.HasSuffix(dbName, "_id"):
			return "" // handled by fixture or generated
		default:
			return fmt.Sprintf(`"e2e_test_%s_" + testUniqueID`, dbName)
		}
	case "bool":
		return "false"
	case "int", "int32", "int64":
		return "0"
	default:
		return ""
	}
}

// ─── generation ─────────────────────────────────────────────────────────────

// GenerateE2ETest produces the E2E test files for an entity.
func GenerateE2ETest(data *E2ETestData, testDir string, opts Options) error {
	if !data.HasCreate && !data.HasGet && !data.HasList && !data.HasDelete {
		return nil
	}

	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	genFiles := []genFile{
		{data.EntityLower + "_generated_test.go", e2eTestGeneratedTemplate, false},
		{data.EntityLower + "_test.go", e2eTestBootstrapTemplate, true},
	}

	for _, f := range genFiles {
		path := filepath.Join(testDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderE2ETestTemplate(f.tmpl, data)
		if err != nil {
			return fmt.Errorf("render %s for %s: %w", f.name, data.EntityName, err)
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

func renderE2ETestTemplate(tmplStr string, data *E2ETestData) ([]byte, error) {
	funcMap := template.FuncMap{
		"lower":    strings.ToLower,
		"camel":    ToCamelCase,
		"testPath": testPathExpr,
	}

	t, err := template.New("e2e_test").Funcs(funcMap).Parse(tmplStr)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// testPathExpr converts "/users/:user_id/delete" + "created." + "UserID"
// into Go expression: "/users/" + created.UserID + "/delete"
func testPathExpr(path, prefix, fieldName string) string {
	idx := strings.Index(path, ":")
	if idx == -1 {
		return fmt.Sprintf(`"%s"`, path)
	}

	routePrefix := path[:idx]
	rest := path[idx:]
	varExpr := prefix + fieldName

	// Find end of :param (next / or end of string).
	paramEnd := strings.Index(rest, "/")
	if paramEnd == -1 {
		// No suffix after param: "/users/" + created.UserID
		return fmt.Sprintf(`"%s" + %s`, routePrefix, varExpr)
	}

	// Has suffix after param: "/users/" + created.UserID + "/delete"
	suffix := rest[paramEnd:]
	return fmt.Sprintf(`"%s" + %s + "%s"`, routePrefix, varExpr, suffix)
}
