package sqlguard

import (
	"os"
	"path/filepath"
	"testing"
)

func scanSource(t *testing.T, src string) []Finding {
	t.Helper()
	root := t.TempDir()
	storeDir := filepath.Join(root, "core", "repositories", "app", "things", "thingsstore")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return findings
}

func TestFlagsDynamicConcatenationIntoSQL(t *testing.T) {
	findings := scanSource(t, `package thingsstore

func bad(userInput string) string {
	return "SELECT * FROM things WHERE name = '" + userInput + "'"
}
`)
	if len(findings) != 1 || findings[0].Kind != "concat" {
		t.Fatalf("want one concat finding, got %+v", findings)
	}
}

func TestFlagsSprintfIntoSQL(t *testing.T) {
	findings := scanSource(t, `package thingsstore

import "fmt"

func bad(order string) string {
	return "SELECT * FROM things ORDER BY " + fmt.Sprintf("%s", order)
}
`)
	if len(findings) != 1 || findings[0].Kind != "concat" {
		t.Fatalf("want one concat finding, got %+v", findings)
	}
}

func TestAllowsSanctionedBuilders(t *testing.T) {
	findings := scanSource(t, `package thingsstore

import "strings"

type args struct{}

func (args) Add(v any) string { return "?" }

func ok(a args, id string, clauses []string) string {
	q := "SELECT * FROM things WHERE id = " + a.Add(id)
	q += "UPDATE things SET " + strings.Join(clauses, ", ")
	return q
}
`)
	if len(findings) != 0 {
		t.Fatalf("sanctioned builders must not be flagged, got %+v", findings)
	}
}

func TestAllowsNonSQLConcatenation(t *testing.T) {
	findings := scanSource(t, `package thingsstore

func ok(name string) string {
	return "hello " + name
}
`)
	if len(findings) != 0 {
		t.Fatalf("non-SQL concatenation must not be flagged, got %+v", findings)
	}
}

func TestFlagsPredRaw(t *testing.T) {
	findings := scanSource(t, `package thingsstore

type Pred struct {
	Raw func() (string, error)
}

func preds() []Pred {
	return []Pred{{Raw: nil}}
}
`)
	if len(findings) != 1 || findings[0].Kind != "raw" {
		t.Fatalf("want one raw finding, got %+v", findings)
	}
}

func TestMissingReposDirIsClean(t *testing.T) {
	root := t.TempDir()
	findings, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("want no findings, got %+v", findings)
	}
}

func TestAllowsPackageLevelConstFragments(t *testing.T) {
	findings := scanSource(t, `package thingsstore

const expansionCTE = `+"`"+`WITH RECURSIVE x AS (SELECT 1)`+"`"+`

func ok() string {
	return expansionCTE + " SELECT * FROM things WHERE id = @id"
}
`)
	if len(findings) != 0 {
		t.Fatalf("package-level const fragments must not be flagged, got %+v", findings)
	}
}
