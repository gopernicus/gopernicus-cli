package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopernicus/gopernicus-cli/internal/schema"
)

// writeBridgeYML drops a bridge.yml at the path GenerateBridge reads.
func writeBridgeYML(t *testing.T, root, domain, table, body string) {
	t.Helper()
	dir := BridgeDir(domain, table, root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bridge.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gateResolved() *ResolvedFile {
	return &ResolvedFile{
		Table:       &schema.TableInfo{TableName: "widgets"},
		TableName:   "widgets",
		DomainName:  "things",
		PackageName: "widgets",
		EntityName:  "Widget",
		PKColumn:    "id",
		PKGoName:    "ID",
		PKGoType:    "string",
	}
}

func TestBridgeRejectsAuthenticateWithoutAuthFeature(t *testing.T) {
	root := t.TempDir()
	writeBridgeYML(t, root, "things", "widgets", `entity: Widget
repo: things/widgets
domain: things
routes:
  - func: List
    path: /widgets
    middleware:
      - authenticate: user
`)

	_, err := GenerateBridge(gateResolved(), "things", "github.com/x/app", root, false, "", false, Options{})
	if err == nil {
		t.Fatal("expected an error for authenticate: on a project without the authentication feature")
	}
	if !strings.Contains(err.Error(), "authentication feature") {
		t.Fatalf("error should explain the missing authentication feature, got: %v", err)
	}
}

func TestBridgeRejectsAuthorizeWithoutAuthFeature(t *testing.T) {
	root := t.TempDir()
	writeBridgeYML(t, root, "things", "widgets", `entity: Widget
repo: things/widgets
domain: things
routes:
  - func: Get
    path: /widgets/{id}
    middleware:
      - authorize: {type: widget, permission: read, param: id}
`)

	_, err := GenerateBridge(gateResolved(), "things", "github.com/x/app", root, false, "", false, Options{})
	if err == nil {
		t.Fatal("expected an error for authorize: on a project without the authentication feature")
	}
	if !strings.Contains(err.Error(), "authentication feature") {
		t.Fatalf("error should explain the missing authentication feature, got: %v", err)
	}
}
