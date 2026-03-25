package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"text/template"
)

// CompositeEntity describes a single entity for the domain composite.
type CompositeEntity struct {
	FieldName  string // PascalCase singular, e.g. "User", "APIKey"
	RepoPkg    string // repo package name, e.g. "users", "apikeys"
	StorePkg   string // pgx store package name, e.g. "userspgx", "apikeyspgx"
	EntityName string // same as FieldName (for template clarity)
	HasEvents  bool   // true if any query has @event annotation
}

// CompositeTemplateData holds all data needed to render domain composite templates.
type CompositeTemplateData struct {
	DomainPkg     string            // domain package name, e.g. "auth", "rebac"
	ModulePath    string            // Go module path (for local imports only)
	FrameworkPath string            // gopernicus framework module path (for auth, infra imports)
	DomainPath    string            // import path segment, e.g. "core/repositories/auth"
	Entities      []CompositeEntity // sorted by FieldName
	HasEvents     bool              // true if any entity in this domain has events
	HasAuth       bool              // true if domain has authorization schema (@auth.relation/@auth.permission annotations)
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

	genFiles := []genFile{
		{"generated_composite.go", compositeGeneratedTemplate, false},
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

// BuildCompositeEntity creates a CompositeEntity from a resolved file.
func BuildCompositeEntity(resolved *ResolvedFile) CompositeEntity {
	return CompositeEntity{
		FieldName:  resolved.EntityName,
		RepoPkg:    resolved.PackageName,
		StorePkg:   resolved.StorePkg,
		EntityName: resolved.EntityName,
		HasEvents:  true, // always available — custom methods may need the event bus
	}
}

func renderCompositeTemplate(tmplStr string, data CompositeTemplateData) ([]byte, error) {
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
