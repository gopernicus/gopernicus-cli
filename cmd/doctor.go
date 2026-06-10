package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/goversion"
	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/project"
	"github.com/gopernicus/gopernicus-cli/internal/sqlguard"
)

func init() {
	RegisterCommand(&Command{
		Name:  "doctor",
		Short: "Check project health and configuration",
		Long: `Check that your project is correctly configured for gopernicus.

Verifies:
  - go.mod exists and is a valid Go module
  - Go version meets minimum requirement
  - gopernicus.yml manifest exists and is valid
  - Workshop directory exists
  - Gopernicus framework is in go.mod dependencies`,
		Usage: "gopernicus doctor",
		Run:   runDoctor,
	})
}

type check struct {
	name   string
	passed bool
	detail string
	warn   bool // warning, not failure
}

func runDoctor(_ context.Context, _ []string) error {
	root, err := project.FindRoot()
	if err != nil {
		fmt.Println("✗ project root — no go.mod found in current or parent directories")
		return fmt.Errorf("doctor found problems")
	}
	fmt.Printf("  project root: %s\n\n", root)

	checks := []check{
		checkGoMod(root),
		checkGoVersion(root),
		checkManifest(root),
		checkWorkshopDir(root),
		checkFrameworkDep(root),
	}
	checks = append(checks, checkSQLGuards(root)...)
	checks = append(checks, checkBodyLimits(root))

	allPassed := true
	for _, c := range checks {
		symbol := "✓"
		if !c.passed && !c.warn {
			symbol = "✗"
			allPassed = false
		} else if c.warn {
			symbol = "!"
		}
		line := fmt.Sprintf("%s %s", symbol, c.name)
		if c.detail != "" {
			line += fmt.Sprintf(" — %s", c.detail)
		}
		fmt.Println(line)
	}

	fmt.Println()
	if !allPassed {
		fmt.Println("Some checks failed. Run 'gopernicus init' to set up a project.")
		return fmt.Errorf("doctor found problems")
	}
	fmt.Println("All checks passed.")
	return nil
}

func checkGoMod(root string) check {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return check{name: "go.mod", passed: false, detail: "not found"}
	}
	return check{name: "go.mod", passed: true}
}

func checkGoVersion(root string) check {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return check{name: "go version", passed: false, detail: "could not read go.mod"}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "go ") {
			v := strings.TrimPrefix(line, "go ")
			v = strings.Fields(v)[0] // strip any inline comments
			name := fmt.Sprintf("go version (go %s)", v)
			if !goversion.MeetsMinimum(v, goversion.MinGoVersion) {
				return check{name: name, passed: false, detail: fmt.Sprintf("requires go %s or later", goversion.MinGoVersion)}
			}
			return check{name: name, passed: true}
		}
	}
	return check{name: "go version", passed: false, detail: "not found in go.mod"}
}

func checkManifest(root string) check {
	if _, err := manifest.Load(root); err != nil {
		return check{name: "gopernicus.yml", passed: false, detail: "not found — run 'gopernicus init'"}
	}
	return check{name: "gopernicus.yml", passed: true}
}

func checkWorkshopDir(root string) check {
	if _, err := os.Stat(filepath.Join(root, "workshop/migrations")); err != nil {
		return check{name: "workshop/migrations/", passed: false, detail: "not found — run 'gopernicus init'"}
	}
	return check{name: "workshop/migrations/", passed: true}
}

// checkSQLGuards runs the standing SQL-injection guard over store packages:
// unsanctioned dynamic concatenation into SQL fails; Pred.Raw usage warns
// (it is a legitimate escape hatch that deserves review on every run).
func checkSQLGuards(root string) []check {
	findings, err := sqlguard.Scan(root)
	if err != nil {
		return []check{{name: "sql guards", passed: false, detail: err.Error()}}
	}

	var concat, raw []sqlguard.Finding
	for _, f := range findings {
		if f.Kind == "raw" {
			raw = append(raw, f)
		} else {
			concat = append(concat, f)
		}
	}

	checks := make([]check, 0, 2)
	if len(concat) == 0 {
		checks = append(checks, check{name: "sql: parameterized queries", passed: true})
	} else {
		detail := concat[0].Pos + " " + concat[0].Message
		if len(concat) > 1 {
			detail = fmt.Sprintf("%s (+%d more)", detail, len(concat)-1)
		}
		checks = append(checks, check{name: "sql: parameterized queries", passed: false, detail: detail})
	}
	if len(raw) > 0 {
		detail := raw[0].Pos + " " + raw[0].Message
		if len(raw) > 1 {
			detail = fmt.Sprintf("%s (+%d more)", detail, len(raw)-1)
		}
		checks = append(checks, check{name: "sql: Pred.Raw usage", passed: true, warn: true, detail: detail})
	}
	return checks
}

// checkBodyLimits warns when a write route (Create/Update) in any bridge.yml
// has no max_body_size middleware — unbounded request bodies are a
// resource-exhaustion vector (P6). A warning, not a failure: the default
// limit may be intentional.
func checkBodyLimits(root string) check {
	var unbounded []string

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "bridge.yml" {
			return nil
		}
		yml, perr := generators.ParseBridgeYML(path)
		if perr != nil || yml == nil {
			return nil
		}
		for _, route := range yml.Routes {
			if route.Func != "Create" && route.Func != "Update" {
				continue
			}
			hasLimit := false
			for _, mw := range route.Middleware {
				if mw.MaxBodySize > 0 {
					hasLimit = true
					break
				}
			}
			if !hasLimit {
				rel, _ := filepath.Rel(root, path)
				unbounded = append(unbounded, fmt.Sprintf("%s:%s", rel, route.Func))
			}
		}
		return nil
	})

	if len(unbounded) == 0 {
		return check{name: "bridge: write routes bound body size", passed: true}
	}
	detail := unbounded[0] + " has no max_body_size"
	if len(unbounded) > 1 {
		detail = fmt.Sprintf("%s (+%d more)", detail, len(unbounded)-1)
	}
	return check{name: "bridge: write routes bound body size", passed: true, warn: true, detail: detail}
}

func checkFrameworkDep(root string) check {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return check{name: "gopernicus dependency", passed: false, detail: "could not read go.mod"}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, generators.FrameworkModulePath) {
			// Extract version from the require line.
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return check{name: fmt.Sprintf("gopernicus framework (%s)", parts[len(parts)-1]), passed: true}
			}
			return check{name: "gopernicus framework", passed: true}
		}
	}
	return check{
		name:   "gopernicus framework",
		passed: false,
		detail: "not found in go.mod — run 'go get " + generators.FrameworkModulePath + "@latest'",
	}
}
