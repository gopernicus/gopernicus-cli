package generators

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// CompositeEntity describes a single entity for the domain composite.
type CompositeEntity struct {
	FieldName  string // PascalCase singular, e.g. "User", "APIKey"
	RepoPkg    string // repo package name, e.g. "users", "apikeys"
	StorePkg   string // store package name, e.g. "userspgx", "usersstore"
	EntityName string // same as FieldName (for template clarity)

	// CacheKeyPrefix qualifies the entity's cache keys with the hosting
	// database (e.g. "primary:events:event_outbox"). Set only for entities
	// hosted by more than one database; empty keeps the entity's default
	// prefix via NewCacheStore.
	CacheKeyPrefix string
}

// CompositeTemplateData holds all data needed to render domain composite templates.
type CompositeTemplateData struct {
	DomainPkg     string            // domain package name, e.g. "auth", "rebac"
	ModulePath    string            // Go module path (for local imports only)
	FrameworkPath string            // gopernicus framework module path (for auth, infra imports)
	DomainPath    string            // import path segment, e.g. "core/repositories/auth"
	Entities      []CompositeEntity // sorted by FieldName
	HasAuth       bool              // true if domain has authorization schema (@auth.relation/@auth.permission annotations)
	SpecMode      bool              // true when the hosting database uses the spec store mode

	// Multi-database rendering controls, set by GenerateDomainComposites for
	// per-database constructor files. Zero values render the classic
	// single-database composite.
	ConstructorSuffix string // exported db suffix, e.g. "Primary" → NewRepositoriesPrimary
	DBName            string // hosting database name (doc comments)
	OmitTypes         bool   // skip shared type decls (they live in generated_composite.go)
}

// DBComposite pairs a hosting database with its composite render data for one domain.
type DBComposite struct {
	DBName string
	Data   CompositeTemplateData
}

// compositeSharedTypesData feeds compositeSharedTypesTemplate.
type compositeSharedTypesData struct {
	DomainPkg     string
	ModulePath    string
	FrameworkPath string
	DomainPath    string
	DBList        string            // hosting databases for doc comments, e.g. "primary, otherdb"
	Entities      []CompositeEntity // union across hosting databases, sorted by FieldName
	NeedsTxRunner bool              // any hosting database uses the spec store mode
}

// GenerateComposite produces domain-level composite wiring files.
// It generates generated_composite.go (always regenerated) and composite.go (bootstrap).
func GenerateComposite(data CompositeTemplateData, domainDir string, opts Options) error {
	if len(data.Entities) == 0 {
		return nil
	}

	// Sort entities by field name for deterministic output.
	sort.Slice(data.Entities, func(i, j int) bool {
		return data.Entities[i].FieldName < data.Entities[j].FieldName
	})

	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	compositeTmpl := compositeGeneratedTemplate
	if data.SpecMode {
		compositeTmpl = compositeSpecGeneratedTemplate
	}

	genFiles := []genFile{
		{"generated_composite.go", compositeTmpl, false},
	}

	for _, f := range genFiles {
		path := filepath.Join(domainDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderCompositeTemplate(f.tmpl, data)
		if err != nil {
			return fmt.Errorf("render %s for %s: %w", f.name, data.DomainPkg, err)
		}

		if err := renderGoFile(f.name, out, path, opts); err != nil {
			return err
		}
	}

	return nil
}

// GenerateDomainComposites renders composite wiring for one domain across its
// hosting databases (callers pass them in canonical order — "primary" first,
// then sorted). One hosting database keeps today's output: a single
// generated_composite.go declaring Repositories and NewRepositories. Multiple
// hosting databases emit generated_composite.go with the shared types plus
// one generated_composite_<db>.go per database holding a NewRepositories<Db>
// constructor in that database's store mode (pgx or spec). Stale
// generated_composite_<db>.go files from previous runs are removed.
func GenerateDomainComposites(domainDir string, composites []DBComposite, opts Options) error {
	if len(composites) == 0 {
		return nil
	}

	if len(composites) == 1 {
		if err := GenerateComposite(composites[0].Data, domainDir, opts); err != nil {
			return err
		}
		return removeStaleDBCompositeFiles(domainDir, nil, opts)
	}

	shared := buildSharedTypesData(composites)
	if err := renderCompositeFile(compositeSharedTypesTemplate, shared,
		filepath.Join(domainDir, "generated_composite.go"), opts); err != nil {
		return fmt.Errorf("shared composite types for %s: %w", shared.DomainPkg, err)
	}

	written := make(map[string]bool, len(composites))
	for _, comp := range composites {
		data := comp.Data
		data.OmitTypes = true
		data.DBName = comp.DBName
		data.ConstructorSuffix = DBExportName(comp.DBName)
		sort.Slice(data.Entities, func(i, j int) bool {
			return data.Entities[i].FieldName < data.Entities[j].FieldName
		})

		tmpl := compositeGeneratedTemplate
		if data.SpecMode {
			tmpl = compositeSpecGeneratedTemplate
		}

		name := DBCompositeFileName(comp.DBName)
		if err := renderCompositeFile(tmpl, data, filepath.Join(domainDir, name), opts); err != nil {
			return fmt.Errorf("composite %s for database %s: %w", data.DomainPkg, comp.DBName, err)
		}
		written[name] = true
	}

	return removeStaleDBCompositeFiles(domainDir, written, opts)
}

// buildSharedTypesData unions the entities across all hosting databases for
// the shared Repositories struct.
func buildSharedTypesData(composites []DBComposite) compositeSharedTypesData {
	first := composites[0].Data
	shared := compositeSharedTypesData{
		DomainPkg:     first.DomainPkg,
		ModulePath:    first.ModulePath,
		FrameworkPath: first.FrameworkPath,
		DomainPath:    first.DomainPath,
	}

	dbNames := make([]string, 0, len(composites))
	seen := make(map[string]bool)
	for _, comp := range composites {
		dbNames = append(dbNames, comp.DBName)
		if comp.Data.SpecMode {
			shared.NeedsTxRunner = true
		}
		for _, e := range comp.Data.Entities {
			if seen[e.FieldName] {
				continue
			}
			seen[e.FieldName] = true
			shared.Entities = append(shared.Entities, e)
		}
	}
	sort.Slice(shared.Entities, func(i, j int) bool {
		return shared.Entities[i].FieldName < shared.Entities[j].FieldName
	})
	shared.DBList = strings.Join(dbNames, ", ")
	return shared
}

// renderCompositeFile renders a composite template, gofmts it, and writes it.
func renderCompositeFile(tmplStr string, data any, path string, opts Options) error {
	out, err := renderCompositeTemplate(tmplStr, data)
	if err != nil {
		return fmt.Errorf("render %s: %w", filepath.Base(path), err)
	}
	return renderGoFile(filepath.Base(path), out, path, opts)
}

// removeStaleDBCompositeFiles deletes generated_composite_<db>.go files this
// run did not produce — e.g. after a domain moves from multi-database hosting
// back to a single database, or a database is renamed. Only files carrying
// the gopernicus generated-code header are touched.
func removeStaleDBCompositeFiles(domainDir string, written map[string]bool, opts Options) error {
	matches, err := filepath.Glob(filepath.Join(domainDir, "generated_composite_*.go"))
	if err != nil {
		return err
	}
	for _, path := range matches {
		if written[filepath.Base(path)] {
			continue
		}
		head, err := os.ReadFile(path)
		if err != nil || !bytes.HasPrefix(head, []byte("// Code generated by gopernicus")) {
			continue
		}
		if opts.DryRun {
			fmt.Printf("  (dry-run) remove %s (stale per-database composite)\n", path)
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove stale %s: %w", path, err)
		}
		if opts.Verbose {
			fmt.Printf("      removed %s (stale per-database composite)\n", path)
		}
	}
	return nil
}

func renderCompositeTemplate(tmplStr string, data any) ([]byte, error) {
	t, err := template.New("composite").Parse(tmplStr)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
