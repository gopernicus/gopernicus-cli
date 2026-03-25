package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
	"text/template"
)

// ─── cache template data types ──────────────────────────────────────────────

// CachedMethod describes a read method that should be cached.
type CachedMethod struct {
	Name       string // e.g. "Get", "GetWithProfile"
	Params     string // full param list
	CallArgs   string // args to delegate to Storer
	ReturnType string // e.g. "User", "GetWithProfileResult"
	KeySegment string // cache key segment, e.g. "get", "get-with-profile"
	KeyExpr    string // Go expression for the cache key value, e.g. "userID"
	TTLExpr    string // Go duration expression, e.g. "5 * time.Minute"
}

// WriteMethod describes a write method that triggers cache invalidation.
type WriteMethod struct {
	Name          string // e.g. "Update", "SoftDelete"
	Params        string // full param list
	CallArgs      string // args to delegate to Storer
	ReturnsEntity bool   // true if returns (Entity, error), false if returns error
	PKExpr        string // Go expression for PK value, e.g. "userID"
}

// CacheTemplateData holds all data needed to render cache templates.
type CacheTemplateData struct {
	PackageName   string
	EntityName    string
	KeyPrefix     string // cache key prefix, e.g. "users"
	CachedMethods []CachedMethod
	WriteMethods  []WriteMethod
}

// GenerateCache produces cache layer files for a repository.
// Always generates the cache wrapper (even with no @cache annotations) so
// that wiring is consistent and adding caching later is just a regenerate.
func GenerateCache(resolved *ResolvedFile, repoDir string, opts Options) (bool, error) {
	data, err := buildCacheData(resolved)
	if err != nil {
		return false, fmt.Errorf("cache: %w", err)
	}

	type genFile struct {
		name      string
		tmpl      string
		bootstrap bool
	}

	genFiles := []genFile{
		{"generated_cache.go", cacheGeneratedTemplate, false},
		{"cache.go", cacheBootstrapTemplate, true},
	}

	for _, f := range genFiles {
		path := filepath.Join(repoDir, f.name)
		if f.bootstrap && fileExists(path) && !opts.ForceBootstrap {
			if opts.Verbose {
				fmt.Printf("      skip %s (already exists)\n", f.name)
			}
			continue
		}

		out, err := renderCacheTemplate(f.tmpl, data)
		if err != nil {
			return false, fmt.Errorf("render %s for %s: %w", f.name, resolved.TableName, err)
		}

		formatted, err := format.Source(out)
		if err != nil {
			_ = writeFile(path, out, opts)
			return false, fmt.Errorf("go/format %s: %w\nUnformatted output written for debugging.", f.name, err)
		}

		if err := writeFile(path, formatted, opts); err != nil {
			return false, err
		}
	}

	return len(data.CachedMethods) > 0, nil
}

func buildCacheData(resolved *ResolvedFile) (CacheTemplateData, error) {
	data := CacheTemplateData{
		PackageName: RepoPackage(resolved.TableName),
		EntityName:  resolved.EntityName,
		KeyPrefix:   resolved.DomainName + ":" + resolved.TableName,
	}

	// Build method signatures from resolved queries (reuse the repo method builder logic).
	repoMethods, err := buildRepoMethods(resolved)
	if err != nil {
		return CacheTemplateData{}, err
	}

	for i, rq := range resolved.Queries {
		m := repoMethods[i]

		// Cached read methods: scan_one or scan_one_custom with @cache annotation.
		if rq.CacheTTL != "" && (m.Category == "scan_one" || m.Category == "scan_one_custom") {
			returnType := resolved.EntityName
			if m.Category == "scan_one_custom" {
				returnType = m.ReturnTypeName
			}

			// Build the cache key expression from the PK params.
			keyExpr := "fmt.Sprint(" + strings.Join(m.PKParams, ", ") + ")"
			if len(m.PKParams) == 1 {
				keyExpr = m.PKParams[0]
			}

			data.CachedMethods = append(data.CachedMethods, CachedMethod{
				Name:       m.Name,
				Params:     m.Params,
				CallArgs:   m.CallArgs,
				ReturnType: returnType,
				KeySegment: strings.ToLower(strings.ReplaceAll(PascalToSpaced(m.Name), " ", "-")),
				KeyExpr:    keyExpr,
				TTLExpr:    rq.CacheTTL,
			})
		}

		// Write methods that take the PK: create, update, update_returning, exec, scan_one without cache.
		// These need invalidation if they modify data and reference the PK.
		switch m.Category {
		case "create":
			// Create returns entity — PK is in the result, not a param.
			// We invalidate nothing on create since there's no prior cached entry.
			continue
		case "update", "update_returning", "exec":
			if !hasPKParam(m.PKParams, resolved.PKColumn) {
				continue
			}
			pkExpr := ToCamelCase(resolved.PKColumn)
			returnsEntity := m.Category == "update_returning"
			data.WriteMethods = append(data.WriteMethods, WriteMethod{
				Name:          m.Name,
				Params:        m.Params,
				CallArgs:      m.CallArgs,
				ReturnsEntity: returnsEntity,
				PKExpr:        pkExpr,
			})
		}
	}

	return data, nil
}

// hasPKParam checks if the PK column name appears in the method's params.
// Deprecated: use FindPKParam instead for new code.
func hasPKParam(pkParams []string, pkColumn string) bool {
	return FindPKParam(pkParams, pkColumn) != ""
}

func renderCacheTemplate(tmplStr string, data CacheTemplateData) ([]byte, error) {
	t, err := template.New("cache").Parse(tmplStr)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
