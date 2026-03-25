package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/goversion"
	"github.com/gopernicus/gopernicus-cli/internal/manifest"
	"github.com/gopernicus/gopernicus-cli/internal/project"
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
		return nil
	}
	fmt.Printf("  project root: %s\n\n", root)

	checks := []check{
		checkGoMod(root),
		checkGoVersion(root),
		checkManifest(root),
		checkWorkshopDir(root),
		checkFrameworkDep(root),
	}

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
	if allPassed {
		fmt.Println("All checks passed.")
	} else {
		fmt.Println("Some checks failed. Run 'gopernicus init' to set up a project.")
	}
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

func checkFrameworkDep(root string) check {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return check{name: "gopernicus dependency", passed: false, detail: "could not read go.mod"}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "github.com/gopernicus/gopernicus") {
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
		detail: "not found in go.mod — run 'go get github.com/gopernicus/gopernicus@latest'",
	}
}
