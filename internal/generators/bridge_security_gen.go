package generators

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"text/template"
)

// SecurityRoute is one authenticated route to probe: requests without and
// with malformed credentials must both be rejected with 401.
type SecurityRoute struct {
	Name string // test-name fragment (route func name)
	Call string // testhttp client call expr, e.g. `client.Get(t, "/x")`
}

// BridgeSecurityData renders generated_security_test.go for one bridged
// entity that has authenticate:-protected routes.
type BridgeSecurityData struct {
	BridgePackage string
	EntityName    string
	FrameworkPath string
	Routes        []SecurityRoute
}

// GenerateBridgeSecurity emits auth-enforcement probes (P1) for every route
// carrying authenticate: middleware. Execution is gated on the
// setupSecurityServer bootstrap — until the project wires its authenticated
// stack, the probes skip loudly rather than fake a pass. Entities without
// authenticated routes get nothing (stale files removed).
func GenerateBridgeSecurity(data BridgeTemplateData, bridgeDir string, opts Options) error {
	path := filepath.Join(bridgeDir, "generated_security_test.go")

	sec := buildBridgeSecurityData(data)
	if len(sec.Routes) == 0 {
		if fileExists(path) && !opts.DryRun {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove stale generated_security_test.go: %w", err)
			}
		}
		return nil
	}

	if err := renderSecurityFile(path, bridgeSecurityGeneratedTemplate, sec, opts); err != nil {
		return err
	}
	fmt.Printf("      write %s\n", path)

	bootstrapPath := filepath.Join(bridgeDir, "security_test.go")
	if !fileExists(bootstrapPath) || opts.ForceBootstrap {
		if err := renderSecurityFile(bootstrapPath, bridgeSecurityBootstrapTemplate, sec, opts); err != nil {
			return err
		}
		fmt.Printf("      create %s\n", bootstrapPath)
	}
	return nil
}

func buildBridgeSecurityData(data BridgeTemplateData) BridgeSecurityData {
	sec := BridgeSecurityData{
		BridgePackage: data.BridgePackage,
		EntityName:    data.EntityName,
		FrameworkPath: gopernicusFrameworkPath,
	}
	for _, r := range data.Routes {
		authenticated := false
		for _, m := range r.MiddlewareChain {
			if m.Authenticate != "" {
				authenticated = true
				break
			}
		}
		if !authenticated {
			continue
		}
		call, ok := probeCall(r.Method, substituteProbeParams(r.Path))
		if !ok {
			continue
		}
		sec.Routes = append(sec.Routes, SecurityRoute{
			Name: r.FuncName,
			Call: call,
		})
	}
	return sec
}

// probeCall renders the testhttp client invocation for a method+path.
func probeCall(method, path string) (string, bool) {
	switch method {
	case "GET":
		return fmt.Sprintf("client.Get(t, %q)", path), true
	case "DELETE":
		return fmt.Sprintf("client.Delete(t, %q)", path), true
	case "POST":
		return fmt.Sprintf("client.Post(t, %q, nil)", path), true
	case "PUT":
		return fmt.Sprintf("client.Put(t, %q, nil)", path), true
	case "PATCH":
		return fmt.Sprintf("client.Patch(t, %q, nil)", path), true
	default:
		return "", false
	}
}

// substituteProbeParams replaces every {param} with a literal probe value —
// enforcement must reject the request BEFORE the resource is resolved, so
// any syntactically valid id works.
func substituteProbeParams(path string) string {
	var out []byte
	for i := 0; i < len(path); i++ {
		if path[i] == '{' {
			for i < len(path) && path[i] != '}' {
				i++
			}
			out = append(out, []byte("probe-id")...)
			continue
		}
		out = append(out, path[i])
	}
	return string(out)
}

func renderSecurityFile(path, tmplText string, sec BridgeSecurityData, opts Options) error {
	tmpl, err := template.New("bridge_security").Parse(tmplText)
	if err != nil {
		return fmt.Errorf("parse security template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, sec); err != nil {
		return fmt.Errorf("render %s: %w", path, err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		_ = writeFile(path, buf.Bytes(), opts)
		return fmt.Errorf("go/format %s: %w\nUnformatted output written for debugging.", path, err)
	}
	return writeFile(path, formatted, opts)
}
