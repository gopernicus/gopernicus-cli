package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"text/template"

	"github.com/gopernicus/gopernicus-cli/internal/manifest"
)

// AuthSchemaGeneratorFunc generates auth schema files for a domain.
// resolvedFiles contains the resolved queries files for all entities in the domain.
type AuthSchemaGeneratorFunc func(domainDir, domainPkg, modulePath string, resolvedFiles []*ResolvedFile, opts Options) error

// authSchemaRegistry maps authorization providers to their schema generators.
// Built-in providers are registered here. External providers will be dispatched
// to user extensions via the workshop/extensions/ pattern (future).
var authSchemaRegistry = map[manifest.Feature]AuthSchemaGeneratorFunc{
	manifest.FeatureGopernicus: GenerateAuthSchema,
}

// AuthSchemaTemplateData holds all data needed to render auth schema templates.
type AuthSchemaTemplateData struct {
	DomainPkg          string             // domain package name, e.g. "auth", "rebac"
	ModulePath         string             // Go module path
	FrameworkPath      string             // gopernicus framework module path (for core/auth imports)
	AuthSchemaEntities []AuthSchemaEntity // entities with auth config (nil = generates return nil)
}

// GenerateAuthSchema produces per-domain authorization schema files.
// It reads @auth.relation and @auth.permission annotations from the resolved
// queries files, and generates generated_authschema.go (always regenerated) and
// authschema.go (bootstrap, created once).
func GenerateAuthSchema(domainDir, domainPkg, modulePath string, resolvedFiles []*ResolvedFile, opts Options) error {
	entities := BuildAuthSchemaEntities(resolvedFiles)

	data := AuthSchemaTemplateData{
		DomainPkg:          domainPkg,
		ModulePath:         modulePath,
		FrameworkPath:      goperniculusFrameworkPath,
		AuthSchemaEntities: entities,
	}

	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	genFiles := []genFile{
		{"generated_authschema.go", authSchemaGeneratedTemplate, false},
		{"authschema.go", authSchemaBootstrapTemplate, true},
	}

	for _, f := range genFiles {
		path := filepath.Join(domainDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderAuthSchemaTemplate(f.tmpl, data)
		if err != nil {
			return fmt.Errorf("render %s for %s: %w", f.name, domainPkg, err)
		}

		formatted, err := format.Source(out)
		if err != nil {
			// Write unformatted for debugging.
			_ = writeFile(path, out, opts)
			return fmt.Errorf("go/format %s: %w\nUnformatted output written for debugging.", f.name, err)
		}

		if err := writeFile(path, formatted, opts); err != nil {
			return err
		}

		if opts.Verbose {
			fmt.Printf("      wrote %s\n", f.name)
		}
	}

	return nil
}

func renderAuthSchemaTemplate(tmplStr string, data AuthSchemaTemplateData) ([]byte, error) {
	t, err := template.New("authschema").Parse(tmplStr)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
