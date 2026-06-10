package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func securityTestData(authenticated bool) BridgeTemplateData {
	route := BridgeRoute{FuncName: "Get", Method: "GET", Path: "/things/{id}"}
	if authenticated {
		route.MiddlewareChain = []MiddlewareEntry{{Authenticate: "user"}}
	}
	return BridgeTemplateData{
		BridgePackage: "thingsbridge",
		EntityName:    "Thing",
		Routes: []BridgeRoute{
			route,
			{FuncName: "List", Method: "GET", Path: "/things",
				MiddlewareChain: []MiddlewareEntry{{RateLimit: true}}},
		},
	}
}

func securityTestResolved() *ResolvedFile {
	return &ResolvedFile{
		TableName:   "things",
		DomainName:  "app",
		PackageName: "things",
		EntityName:  "Thing",
		PKColumn:    "id",
	}
}

func TestSecurityProbesEmitForAuthenticatedRoutes(t *testing.T) {
	dir := t.TempDir()
	err := GenerateBridgeSecurity(securityTestData(true), securityTestResolved(), dir, "github.com/x/app", "primary", false, Options{})
	if err != nil {
		t.Fatalf("GenerateBridgeSecurity: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "generated_security_test.go"))
	if err != nil {
		t.Fatalf("expected generated_security_test.go: %v", err)
	}
	content := string(out)
	for _, want := range []string{
		"//go:build security",
		`client.Get(t, "/things/probe-id").RequireStatus(t, 401)`,
		"rejects malformed token",
		"setupSecurityServer",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated security test missing %q", want)
		}
	}
	if strings.Contains(content, "List rejects") {
		t.Error("unauthenticated route List must not get probes")
	}

	if _, err := os.Stat(filepath.Join(dir, "security_test.go")); err != nil {
		t.Errorf("expected security_test.go bootstrap: %v", err)
	}
}

func TestSecurityProbesAbsentWithoutAuthenticatedRoutes(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed a stale file to prove removal.
	stale := filepath.Join(dir, "generated_security_test.go")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := GenerateBridgeSecurity(securityTestData(false), securityTestResolved(), dir, "github.com/x/app", "primary", false, Options{}); err != nil {
		t.Fatalf("GenerateBridgeSecurity: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale generated_security_test.go must be removed when no routes are authenticated")
	}
}
