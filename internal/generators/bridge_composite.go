package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"text/template"
)

// BridgeCompositeEntity describes a single entity for the bridge composite.
type BridgeCompositeEntity struct {
	FieldName string // PascalCase singular, e.g. "User", "APIKey"
	BridgePkg string // bridge package name, e.g. "usersbridge", "apikeysbridge"
}

// BridgeCompositeTemplateData holds all data needed to render bridge composite templates.
type BridgeCompositeTemplateData struct {
	CompositePkg  string                  // composite package name, e.g. "authreposbridge"
	DomainName    string                  // domain name, e.g. "auth"
	ModulePath    string                  // Go module path (for local imports only)
	FrameworkPath string                  // gopernicus framework module path (for sdk, auth, infra imports)
	Entities      []BridgeCompositeEntity // sorted by FieldName
	AuthEnabled   bool                    // true if authentication is enabled
}

// GenerateBridgeComposite produces domain-level bridge composite wiring files.
// It generates generated_composite.go (always regenerated) and composite.go (bootstrap).
func GenerateBridgeComposite(data BridgeCompositeTemplateData, compositeDir string, opts Options) error {
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
		{"generated_composite.go", bridgeCompositeGeneratedTemplate, false},
	}

	for _, f := range genFiles {
		path := filepath.Join(compositeDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderBridgeCompositeTemplate(f.tmpl, data)
		if err != nil {
			return fmt.Errorf("render %s for %s: %w", f.name, data.CompositePkg, err)
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

// BuildBridgeCompositeEntity creates a BridgeCompositeEntity from a resolved file.
func BuildBridgeCompositeEntity(resolved *ResolvedFile) BridgeCompositeEntity {
	return BridgeCompositeEntity{
		FieldName: resolved.EntityName,
		BridgePkg: BridgePackage(resolved.TableName),
	}
}

// BridgeCompositePackage returns the Go package name for a domain's bridge composite.
func BridgeCompositePackage(domainName string) string {
	return domainName + "reposbridge"
}

// BridgeCompositeDir returns the directory path for a domain's bridge composite.
func BridgeCompositeDir(domainName, outputDir string) string {
	return filepath.Join(outputDir, "bridge", "repositories", BridgeCompositePackage(domainName))
}

func renderBridgeCompositeTemplate(tmplStr string, data BridgeCompositeTemplateData) ([]byte, error) {
	t, err := template.New("bridge_composite").Parse(tmplStr)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
