package generators

import (
	"bytes"
	"fmt"
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

	// SpecMode selects the test stack: spec → testsqlite + sqlitefixtures +
	// NewStore(q,d,inTx); pgx → testpgx + fixtures + NewStore(log, pool).
	// The HTTP-level test bodies are identical either way.
	SpecMode bool

	RepoPkg     string
	RepoImport  string
	StorePkg    string
	StoreImport string

	MigrationsDir string // e.g. "workshop/migrations/litedb"

	// FixturePkg / FixtureImport / FKSeeds drive parent seeding for FK-child
	// entities: the create body's FK fields are populated from rows the
	// fixtures package inserts before the POST.
	FixturePkg    string
	FixtureImport string
	FKSeeds       []FKSeed

	PKJSON string // JSON field name of the PK, e.g. "id"

	// NotFoundID is a syntactically-valid but absent id for the not-found
	// probe — a real UUID for uuid PKs (a non-UUID string would 500 on the
	// pgx driver, not 404), else an arbitrary string.
	NotFoundID string

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

	// Update-path mass-assignment probe (P5): a PUT setting a legit field
	// plus a smuggled record_state must leave the stored state untouched.
	// Requires record_state, an Update route, and a settable update field.
	HasUpdate         bool
	UpdatePathExpr    string // Go expr building the PUT path from `id`
	UpdateLegitJSON   string // a non-server-set update field (json name)
	UpdateLegitValue  string // a valid value literal for that field

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
func GenerateBridgeE2E(data BridgeTemplateData, resolved *ResolvedFile, bridgeDir, modulePath, hostDB string, specMode bool, opts Options) error {
	path := filepath.Join(bridgeDir, "generated_e2e_test.go")

	e2e, ok := buildBridgeE2EData(data, resolved, modulePath, hostDB, specMode)
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
	return renderGoFile(path, buf.Bytes(), path, opts)
}

// FKSeed is one foreign-key create field whose value comes from a parent row
// seeded by the fixtures package before the POST.
type FKSeed struct {
	RequestField string // create-request Go field, e.g. "AccountID"
	ParentEntity string // parent entity name, e.g. "Account"
	ParentPKExpr string // expr off the seeded parent, e.g. "parent0.ID"
}

func buildBridgeE2EData(data BridgeTemplateData, resolved *ResolvedFile, modulePath, hostDB string, specMode bool) (BridgeE2EData, bool) {
	if len(data.CreateQueries) == 0 {
		return BridgeE2EData{}, false
	}

	// Required FK create fields are populated from seeded parents. Each FK
	// column must map to a field in the create request (one that wasn't
	// stripped as a URL path param) and a parent table we can seed.
	seeds, ok := buildFKSeeds(data, resolved)
	if !ok {
		return BridgeE2EData{}, false
	}

	// Store package + fixtures differ by mode; the test bodies do not.
	wiring := buildStackWiring(resolved, modulePath, hostDB, specMode)
	fixturePkg, fixtureDir := "fixtures", "fixtures"
	if specMode {
		fixturePkg, fixtureDir = "sqlitefixtures", "sqlitefixtures"
	}

	e2e := BridgeE2EData{
		BridgePackage: data.BridgePackage,
		EntityName:    data.EntityName,
		FrameworkPath: gopernicusFrameworkPath,
		SpecMode:      specMode,
		RepoPkg:       wiring.RepoPkg,
		RepoImport:    wiring.RepoImport,
		StorePkg:      wiring.StorePkg,
		StoreImport:   wiring.StoreImport,
		FixturePkg:    fixturePkg,
		FixtureImport: modulePath + "/workshop/testing/" + fixtureDir,
		FKSeeds:       seeds,
		MigrationsDir: wiring.MigrationsDir,
		PKJSON:        resolved.PKColumn,
		NotFoundID:    "nonexistent-e2e-id",
	}
	if pkIsUUID(resolved) {
		e2e.NotFoundID = "00000000-0000-4000-8000-000000000000"
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
			// Enum filters have a closed, type-checked domain — an
			// out-of-domain value can't inject (it's bound and rejected by
			// the column type), so they aren't a probe surface. (Postgres
			// 500s on an invalid enum cast; tracked as a robustness item.)
			if f.IsEnum {
				continue
			}
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

	// The e2e probes drive routes anonymously (POST → 201, GET → 200). If
	// any route the suite exercises requires authentication or authorization,
	// those calls would 401/403 instead — skip e2e for the entity until an
	// authenticated stack exists (see the security suite for auth coverage).
	for _, r := range data.Routes {
		for _, m := range r.MiddlewareChain {
			if m.Authenticate != "" || m.Authorize != nil {
				return BridgeE2EData{}, false
			}
		}
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
		case "Update":
			if expr, ok := pkOnlyPathExpr(r.Path, pkParam); ok {
				e2e.HasUpdate = true
				e2e.UpdatePathExpr = expr
			}
		}
	}

	// Pick a settable, non-server-set string update field for the update
	// mass-assignment probe (SEC1 already strips record_state/ownership from
	// the update model, so any remaining string field is a safe legit edit).
	if e2e.HasRecordState && e2e.HasUpdate && len(data.UpdateQueries) > 0 {
		for _, f := range data.UpdateQueries[0].Fields {
			if f.IsString && !f.IsEnum {
				e2e.UpdateLegitJSON = f.DBName
				e2e.UpdateLegitValue = `"edited-value"`
				break
			}
		}
	}

	// Without a paramless POST there is nothing to round-trip.
	if e2e.CreatePath == "" {
		return BridgeE2EData{}, false
	}
	return e2e, true
}

// buildFKSeeds maps each required (NOT NULL) foreign-key create field to a
// parent fixture and the create-request field it fills. Returns ok=false when
// an FK can't be seeded from the request body (e.g. it was stripped as a URL
// path param, or its create field isn't in the request), so the caller falls
// back to skipping e2e for that entity. Self-referential FKs are nullable by
// design and need no seed.
func buildFKSeeds(data BridgeTemplateData, resolved *ResolvedFile) ([]FKSeed, bool) {
	if resolved.Table == nil {
		return nil, true
	}

	// Request fields present in the (path-param-stripped) create model.
	requestFields := map[string]string{} // dbName -> GoName
	for _, f := range data.CreateQueries[0].Fields {
		requestFields[f.DBName] = f.GoName
	}

	// Which create columns are NOT NULL (required for a successful insert).
	required := map[string]bool{}
	for _, rq := range resolved.Queries {
		for _, f := range rq.InsertFields {
			if !f.IsNullable {
				required[f.DBName] = true
			}
		}
	}

	var seeds []FKSeed
	idx := 0
	for _, fk := range resolved.Table.ForeignKeys {
		if len(fk.Columns) != 1 || len(fk.RefColumns) != 1 {
			return nil, false // composite FK — not seedable by this simple path
		}
		col := fk.Columns[0]
		if !required[col] {
			continue // nullable FK — the insert succeeds without it
		}
		if fk.RefTable == resolved.TableName {
			return nil, false // self-ref required FK can't be satisfied
		}
		goName, ok := requestFields[col]
		if !ok {
			return nil, false // FK not settable from the request body
		}
		seeds = append(seeds, FKSeed{
			RequestField: goName,
			ParentEntity: ToPascalCase(Singularize(fk.RefTable)),
			ParentPKExpr: fmt.Sprintf("parent%d.%s", idx, ToPascalCase(fk.RefColumns[0])),
		})
		idx++
	}
	return seeds, true
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

