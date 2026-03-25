package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
	"text/template"
)

// ─── integration test template data types ───────────────────────────────────

// IntegrationTestMethod describes a store method to test.
type IntegrationTestMethod struct {
	Name     string // e.g. "Get", "Create", "List"
	Category string // "scan_one", "create", "list", "update", "update_returning", "exec"

	// For scan_one / get: the PK param name
	PKParam string // e.g. "userID"

	// For create
	HasCreate bool

	// For list
	HasList bool

	// For update
	HasUpdate bool

	// For exec (soft delete, archive, restore, hard delete)
	IsDelete    bool // hard delete
	IsSoftState bool // soft delete / archive / restore
	NewState    string // e.g. "deleted", "archived", "active"

	// For update_returning
	ReturnsEntity bool
}

// IntegrationTestData holds all data for rendering a pgxstore integration test.
type IntegrationTestData struct {
	// Package info
	StorePkg   string // e.g. "userspgx"
	RepoPkg    string // e.g. "users"
	EntityName string // e.g. "User"
	EntityLower string // e.g. "user"

	// Import paths
	RepoImport    string // full import path to repo package
	FixtureImport string // full import path to fixtures package

	// PK info
	PKColumn string // e.g. "user_id"
	PKGoName string // e.g. "UserID"
	PKGoType string // e.g. "string"

	// Methods to test
	Methods []IntegrationTestMethod

	// Feature flags
	HasCreate     bool
	HasGet        bool
	HasList       bool
	HasUpdate     bool
	HasSoftDelete bool
	HasHardDelete bool

	// Domain info
	DomainName string
}

// BuildIntegrationTestData creates test data from a resolved file.
func BuildIntegrationTestData(resolved *ResolvedFile, modulePath string) (IntegrationTestData, error) {
	data := IntegrationTestData{
		StorePkg:      resolved.StorePkg,
		RepoPkg:       resolved.PackageName,
		EntityName:    resolved.EntityName,
		EntityLower:   resolved.EntityLower,
		RepoImport:    modulePath + "/core/repositories/" + resolved.DomainName + "/" + resolved.PackageName,
		FixtureImport: modulePath + "/core/testing/fixtures",
		PKColumn:      resolved.PKColumn,
		PKGoName:      resolved.PKGoName,
		PKGoType:      resolved.PKGoType,
		DomainName:    resolved.DomainName,
	}

	methods, err := buildRepoMethods(resolved)
	if err != nil {
		return IntegrationTestData{}, err
	}

	for i, m := range methods {
		rq := resolved.Queries[i]
		tm := IntegrationTestMethod{
			Name:     m.Name,
			Category: m.Category,
		}

		switch m.Category {
		case "scan_one", "scan_one_custom":
			data.HasGet = true
			if pk := FindPKParam(m.PKParams, resolved.PKColumn); pk != "" {
				tm.PKParam = pk
			}

		case "create":
			data.HasCreate = true
			tm.HasCreate = true

		case "list":
			// Only generate the standard List test for the method named "List"
			// (not "ListByFoo" variants which have different filter types).
			if m.Name == "List" {
				data.HasList = true
				tm.HasList = true
			}

		case "update":
			data.HasUpdate = true
			tm.HasUpdate = true
			// Check for soft-delete state changes.
			nameLower := strings.ToLower(m.Name)
			switch {
			case nameLower == "softdelete":
				tm.IsSoftState = true
				tm.NewState = "deleted"
			case nameLower == "archive":
				tm.IsSoftState = true
				tm.NewState = "archived"
			case nameLower == "restore":
				tm.IsSoftState = true
				tm.NewState = "active"
			}

		case "update_returning":
			data.HasUpdate = true
			tm.HasUpdate = true
			tm.ReturnsEntity = true

		case "exec":
			// Determine if it's a delete or state change.
			if rq.Type == QueryDelete {
				data.HasHardDelete = true
				tm.IsDelete = true
			} else {
				nameLower := strings.ToLower(m.Name)
				switch {
				case nameLower == "softdelete":
					data.HasSoftDelete = true
					tm.IsSoftState = true
					tm.NewState = "deleted"
				case nameLower == "archive":
					tm.IsSoftState = true
					tm.NewState = "archived"
				case nameLower == "restore":
					tm.IsSoftState = true
					tm.NewState = "active"
				}
			}
		}

		data.Methods = append(data.Methods, tm)
	}

	return data, nil
}

// GenerateIntegrationTest produces the generated_test.go file for a pgxstore package.
func GenerateIntegrationTest(data IntegrationTestData, storeDir string, opts Options) error {
	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	genFiles := []genFile{
		{"generated_test.go", integrationTestGeneratedTemplate, false},
		{"store_test.go", integrationTestBootstrapTemplate, true},
	}

	for _, f := range genFiles {
		path := filepath.Join(storeDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderIntegrationTestTemplate(f.tmpl, data)
		if err != nil {
			return fmt.Errorf("render %s for %s: %w", f.name, data.StorePkg, err)
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

func renderIntegrationTestTemplate(tmplStr string, data IntegrationTestData) ([]byte, error) {
	funcMap := template.FuncMap{
		"lower": strings.ToLower,
		"camel": ToCamelCase,
	}

	t, err := template.New("integration_test").Funcs(funcMap).Parse(tmplStr)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
