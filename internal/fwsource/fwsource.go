// Package fwsource resolves the gopernicus framework source directory.
// In dev mode (GOPERNICUS_DEV_SOURCE set), it points to a local checkout.
// In production, it resolves the module from the Go module cache via
// `go mod download -json`.
package fwsource

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const gopernicusModule = "github.com/gopernicus/gopernicus"

// modDownloadResult is the subset of `go mod download -json` output we need.
type modDownloadResult struct {
	Dir string `json:"Dir"`
}

// ResolveDir returns the gopernicus framework source directory.
// It checks GOPERNICUS_DEV_SOURCE first (dev mode), then falls back to
// resolving from the Go module cache via `go mod download -json`.
func ResolveDir() (string, error) {
	if dev := os.Getenv("GOPERNICUS_DEV_SOURCE"); dev != "" {
		if _, err := os.Stat(dev); err != nil {
			return "", fmt.Errorf("GOPERNICUS_DEV_SOURCE %q not found: %w", dev, err)
		}
		return dev, nil
	}

	out, err := exec.Command("go", "mod", "download", "-json", gopernicusModule).Output()
	if err != nil {
		return "", fmt.Errorf("resolving gopernicus module: %w", err)
	}

	var result modDownloadResult
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("parsing go mod download output: %w", err)
	}
	if result.Dir == "" {
		return "", fmt.Errorf("go mod download returned empty Dir for %s", gopernicusModule)
	}
	return result.Dir, nil
}

// ResolveDirVersion is like ResolveDir but fetches a specific version when the
// module is not yet in go.mod (e.g. during `gopernicus init`).
// If version is empty, defaults to "@latest".
func ResolveDirVersion(version string) (string, error) {
	if dev := os.Getenv("GOPERNICUS_DEV_SOURCE"); dev != "" {
		if _, err := os.Stat(dev); err != nil {
			return "", fmt.Errorf("GOPERNICUS_DEV_SOURCE %q not found: %w", dev, err)
		}
		return dev, nil
	}

	versionSuffix := "@latest"
	if version != "" {
		versionSuffix = "@" + version
	}

	out, err := exec.Command("go", "mod", "download", "-json", gopernicusModule+versionSuffix).Output()
	if err != nil {
		return "", fmt.Errorf("resolving gopernicus module %s: %w", versionSuffix, err)
	}

	var result modDownloadResult
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("parsing go mod download output: %w", err)
	}
	if result.Dir == "" {
		return "", fmt.Errorf("go mod download returned empty Dir for %s%s", gopernicusModule, versionSuffix)
	}
	return result.Dir, nil
}

// RepoFiles returns all bootstrap files for a known framework table as a map
// of relative path → content. Paths are relative to the entity directory:
//
//	"queries.sql"
//	"repository.go"
//	"cache.go"
//	"{entity}pgx/store.go"
//	"bridge/bridge.yml"
//	"bridge/bridge.go"
//
// Returns an empty map when the entity directory does not exist.
func RepoFiles(sourceDir, domain, tableName string) map[string][]byte {
	entityPkg := toPackageName(tableName)

	result := make(map[string][]byte)

	// Core repo files: core/repositories/{domain}/{entity}/
	coreDir := filepath.Join(sourceDir, "core", "repositories", domain, entityPkg)
	walkCollect(coreDir, "", result)

	// Bridge files: bridge/repositories/{domain}reposbridge/{entity}bridge/
	bridgeDir := filepath.Join(sourceDir, "bridge", "repositories", domain+"reposbridge", entityPkg+"bridge")
	walkCollect(bridgeDir, "bridge/", result)

	return result
}

// QueriesSQL returns just the queries.sql content for a framework table,
// or (nil, false) if not found.
func QueriesSQL(sourceDir, domain, tableName string) ([]byte, bool) {
	entityPkg := toPackageName(tableName)
	path := filepath.Join(sourceDir, "core", "repositories", domain, entityPkg, "queries.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// walkCollect walks dir and adds all files to result with keys prefixed by prefix.
// Generated files (generated_*.go) are excluded since those are produced by
// `gopernicus generate` and should not be scaffolded.
func walkCollect(dir, prefix string, result map[string][]byte) {
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		// Skip generated files — they'll be recreated by `gopernicus generate`.
		name := d.Name()
		if strings.HasPrefix(name, "generated_") || strings.HasSuffix(name, "_test.go") {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[prefix+filepath.ToSlash(rel)] = data
		return nil
	})
}

// toPackageName mirrors generators.ToPackageName without importing that package.
func toPackageName(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}
