package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// BridgeE2EData renders generated_e2e_test.go for one bridged entity hosted
// by a spec-mode database: a self-contained HTTP stack (testsqlite → spec
// store → repository → bridge → web handler → httptest.Server) driven by the
// entity's resolved bridge.yml routes.
type BridgeE2EData struct {
	BridgePackage string
	EntityName    string
	FrameworkPath string

	RepoPkg     string
	RepoImport  string
	StorePkg    string
	StoreImport string

	MigrationsDir string // e.g. "workshop/migrations/litedb"

	PKJSON string // JSON field name of the PK, e.g. "id"

	CreatePath string // POST path, no params

	// CreateMaxBodySize is the create route's max_body_size in bytes (0 =
	// none) — gates the oversized-payload probe (P6).
	CreateMaxBodySize int64

	HasGet      bool
	GetPathExpr string // Go expr building the GET path from `id`

	HasList  bool
	ListPath string

	HasDelete      bool
	DeletePathExpr string // Go expr building the DELETE path from `id`

	// HasRecordState gates the mass-assignment probe: a POST smuggling a
	// record_state value must not control the stored state (SEC1/P5).
	HasRecordState bool

	// String filter params (plus "search") get the strict probe: a payload
	// is a parameterized match value, so a 200 must return zero rows.
	// Non-string filter params join order/limit/cursor in the never-500
	// probe only — their parsers ignore unparseable values, legitimately
	// returning unfiltered results (P2).
	StringFilterParams []string
	OtherProbeParams   []string
}

// GenerateBridgeE2E emits light e2e tests for a bridged entity hosted by a
// spec-mode database. Entities whose create model requires foreign keys are
// skipped for now — their POST bodies need seeded parents. Routes with
// params other than the PK are likewise skipped (scope params need fixture
// context the plain HTTP round-trip doesn't have).
func GenerateBridgeE2E(data BridgeTemplateData, resolved *ResolvedFile, bridgeDir, modulePath, specDB string, opts Options) error {
	path := filepath.Join(bridgeDir, "generated_e2e_test.go")

	e2e, ok := buildBridgeE2EData(data, resolved, modulePath, specDB)
	if !ok {
		if fileExists(path) && !opts.DryRun {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove stale generated_e2e_test.go: %w", err)
			}
		}
		return nil
	}

	if err := renderE2EFile(path, bridgeE2EGeneratedTemplate, e2e, opts); err != nil {
		return err
	}
	fmt.Printf("      write %s\n", path)

	bootstrapPath := filepath.Join(bridgeDir, "e2e_test.go")
	if !fileExists(bootstrapPath) || opts.ForceBootstrap {
		if err := renderE2EFile(bootstrapPath, bridgeE2EBootstrapTemplate, e2e, opts); err != nil {
			return err
		}
		fmt.Printf("      create %s\n", bootstrapPath)
	}
	return nil
}

func renderE2EFile(path, tmplText string, e2e BridgeE2EData, opts Options) error {
	tmpl, err := template.New("bridge_e2e").Parse(tmplText)
	if err != nil {
		return fmt.Errorf("parse e2e template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, e2e); err != nil {
		return fmt.Errorf("render %s: %w", path, err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		_ = writeFile(path, buf.Bytes(), opts)
		return fmt.Errorf("go/format %s: %w\nUnformatted output written for debugging.", path, err)
	}
	return writeFile(path, formatted, opts)
}

func buildBridgeE2EData(data BridgeTemplateData, resolved *ResolvedFile, modulePath, specDB string) (BridgeE2EData, bool) {
	// A required FK in the create model means the plain POST body cannot
	// satisfy referential integrity without seeded parents.
	if len(data.CreateQueries) == 0 {
		return BridgeE2EData{}, false
	}
	for i := range resolved.Queries {
		rq := resolved.Queries[i]
		for _, f := range rq.InsertFields {
			if f.IsForeignKey && !f.IsNullable && f.DBName != resolved.PKColumn {
				return BridgeE2EData{}, false
			}
		}
	}

	e2e := BridgeE2EData{
		BridgePackage: data.BridgePackage,
		EntityName:    data.EntityName,
		FrameworkPath: gopernicusFrameworkPath,
		RepoPkg:       resolved.PackageName,
		RepoImport:    modulePath + "/core/repositories/" + resolved.DomainName + "/" + resolved.PackageName,
		StorePkg:      StorePackage(resolved.TableName, specStorePackageSuffix),
		StoreImport: modulePath + "/core/repositories/" + resolved.DomainName + "/" +
			resolved.PackageName + "/" + StorePackage(resolved.TableName, specStorePackageSuffix),
		MigrationsDir: "workshop/migrations/" + specDB,
		PKJSON:        resolved.PKColumn,
	}
	for _, col := range resolved.AllColumns {
		if col.Name == "record_state" {
			e2e.HasRecordState = true
			break
		}
	}
	for _, lq := range data.ListQueries {
		if lq.FuncName != "List" {
			continue
		}
		for _, f := range lq.FilterFields {
			if f.IsString {
				e2e.StringFilterParams = append(e2e.StringFilterParams, f.DBName)
			} else {
				e2e.OtherProbeParams = append(e2e.OtherProbeParams, f.DBName)
			}
		}
		if lq.HasSearch {
			e2e.StringFilterParams = append(e2e.StringFilterParams, "search")
		}
		break
	}

	pkParam := "{" + resolved.PKColumn + "}"
	for _, r := range data.Routes {
		switch r.FuncName {
		case "Create":
			if r.Method == "POST" && !strings.Contains(r.Path, "{") {
				e2e.CreatePath = r.Path
				for _, m := range r.MiddlewareChain {
					if m.MaxBodySize > 0 {
						e2e.CreateMaxBodySize = m.MaxBodySize
						break
					}
				}
			}
		case "Get":
			if expr, ok := pkOnlyPathExpr(r.Path, pkParam); ok {
				e2e.HasGet = true
				e2e.GetPathExpr = expr
			}
		case "List":
			if r.Method == "GET" && !strings.Contains(r.Path, "{") {
				e2e.HasList = true
				e2e.ListPath = r.Path
			}
		case "Delete":
			if expr, ok := pkOnlyPathExpr(r.Path, pkParam); ok {
				e2e.HasDelete = true
				e2e.DeletePathExpr = expr
			}
		}
	}

	// Without a paramless POST there is nothing to round-trip.
	if e2e.CreatePath == "" {
		return BridgeE2EData{}, false
	}
	return e2e, true
}

// pkOnlyPathExpr converts a route path whose only parameter is the PK into a
// Go expression over the `id` variable, e.g. "/widgets/{id}" →
// `"/widgets/" + id`. Paths with other params are rejected.
func pkOnlyPathExpr(path, pkParam string) (string, bool) {
	if !strings.Contains(path, pkParam) {
		return "", false
	}
	rest := strings.ReplaceAll(path, pkParam, "")
	if strings.Contains(rest, "{") {
		return "", false
	}
	parts := strings.Split(path, pkParam)
	exprs := make([]string, 0, len(parts)*2-1)
	for i, p := range parts {
		if i > 0 {
			exprs = append(exprs, "id")
		}
		if p != "" {
			exprs = append(exprs, fmt.Sprintf("%q", p))
		}
	}
	return strings.Join(exprs, " + "), true
}

