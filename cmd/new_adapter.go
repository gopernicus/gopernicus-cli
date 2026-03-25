package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gopernicus/gopernicus-cli/internal/project"
)

const frameworkModule = "github.com/gopernicus/gopernicus"

func init() {
	newCmd.SubCommands = append(newCmd.SubCommands, &Command{
		Name:  "adapter",
		Short: "Scaffold a custom adapter for a framework interface",
		Usage: "gopernicus new adapter <type> <name>",
		Run:   runNewAdapter,
	})
}

func runNewAdapter(_ context.Context, args []string) error {
	if len(args) < 2 {
		return adapterUsageError()
	}

	typeName := args[0]
	adapterName := strings.ToLower(args[1])

	spec, ok := adapterSpecs[typeName]
	if !ok {
		return adapterUsageError()
	}

	root, err := project.FindRoot()
	if err != nil {
		return fmt.Errorf("not in a Go project — run 'gopernicus init' first")
	}

	modulePath, err := project.ModulePath(root)
	if err != nil {
		return fmt.Errorf("reading module path: %w", err)
	}

	adapterDir := filepath.Join(root, spec.AdapterDir, adapterName)
	if _, err := os.Stat(adapterDir); err == nil {
		return fmt.Errorf("directory %s already exists", filepath.Join(spec.AdapterDir, adapterName))
	}

	if err := os.MkdirAll(adapterDir, 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	data := templateData{
		PkgName:       adapterName,
		StructName:    spec.StructName,
		ReceiverVar:   spec.ReceiverVar,
		FrameworkMod:  frameworkModule,
		InterfacePkg:  spec.InterfacePkg,
		InterfaceRef:  spec.InterfaceRef,
		SourceImports: spec.SourceImports,
		MethodStubs:   spec.MethodStubs,
		CompliancePkg: spec.CompliancePkg,
		ComplianceCall: strings.ReplaceAll(spec.ComplianceCall, "{{var}}", spec.ReceiverVar),
		DeferClose:    strings.ReplaceAll(spec.DeferClose, "{{var}}", spec.ReceiverVar),
		TestImports:   spec.TestImports,
		UserModule:    modulePath,
		AdapterDir:    spec.AdapterDir,
	}

	// Write source file.
	srcPath := filepath.Join(adapterDir, adapterName+".go")
	if err := writeTemplate(srcPath, sourceTmpl, data); err != nil {
		return fmt.Errorf("writing %s: %w", srcPath, err)
	}

	// Write test file.
	testPath := filepath.Join(adapterDir, adapterName+"_test.go")
	if err := writeTemplate(testPath, testTmpl, data); err != nil {
		return fmt.Errorf("writing %s: %w", testPath, err)
	}

	rel := filepath.Join(spec.AdapterDir, adapterName)
	fmt.Printf("\n  ✓ created %s/\n", rel)
	fmt.Printf("    %s/%s.go\n", rel, adapterName)
	fmt.Printf("    %s/%s_test.go\n\n", rel, adapterName)
	fmt.Printf("  Implement the TODO stubs, then run:\n")
	fmt.Printf("    go test ./%s/...\n\n", rel)

	return nil
}

func adapterUsageError() error {
	types := []string{"cache", "emailer", "events", "hasher", "ratelimiter", "storage", "token"}
	return fmt.Errorf("usage: gopernicus new adapter <type> <name>\n\nAvailable types: %s",
		strings.Join(types, ", "))
}

// =============================================================================
// Template Data & Rendering
// =============================================================================

type templateData struct {
	PkgName        string
	StructName     string
	ReceiverVar    string
	FrameworkMod   string
	InterfacePkg   string
	InterfaceRef   string
	SourceImports  []string
	MethodStubs    string
	CompliancePkg  string
	ComplianceCall string
	DeferClose     string
	TestImports    []string
	UserModule     string
	AdapterDir     string
}

func writeTemplate(path string, tmpl *template.Template, data templateData) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, data)
}

var sourceTmpl = template.Must(template.New("source").Parse(`package {{.PkgName}}

import (
{{- if .SourceImports}}
{{- range .SourceImports}}
	"{{.}}"
{{- end}}

{{- end}}
	"{{.FrameworkMod}}/{{.InterfacePkg}}"
)

// Compile-time interface check.
var _ {{.InterfaceRef}} = (*{{.StructName}})(nil)

// {{.StructName}} implements {{.InterfaceRef}}.
type {{.StructName}} struct {
	// TODO: Add your fields here.
}

// Option configures a {{.StructName}}.
type Option func(*{{.StructName}})

// New creates a new {{.StructName}}.
func New(opts ...Option) *{{.StructName}} {
	{{.ReceiverVar}} := &{{.StructName}}{}
	for _, opt := range opts {
		opt({{.ReceiverVar}})
	}
	return {{.ReceiverVar}}
}

{{.MethodStubs}}
`))

var testTmpl = template.Must(template.New("test").Parse(`package {{.PkgName}}_test

import (
{{- range .TestImports}}
	"{{.}}"
{{- end}}
	"testing"

	"{{.FrameworkMod}}/{{.CompliancePkg}}"
	"{{.UserModule}}/{{.AdapterDir}}/{{.PkgName}}"
)

func TestCompliance(t *testing.T) {
	{{.ReceiverVar}} := {{.PkgName}}.New()
{{- if .DeferClose}}
	defer {{.DeferClose}}
{{- end}}
	{{.ComplianceCall}}
}
`))

// =============================================================================
// Adapter Specifications
// =============================================================================

var adapterSpecs = map[string]adapterSpec{
	"cache": {
		InterfacePkg:  "infrastructure/cache",
		InterfaceRef:  "cache.Cacher",
		StructName:    "Store",
		ReceiverVar:   "s",
		CompliancePkg: "infrastructure/cache/cachetest",
		ComplianceCall: "cachetest.RunSuite(t, {{var}})",
		DeferClose:    "{{var}}.Close()",
		AdapterDir:    "infrastructure/cache",
		SourceImports: []string{"context", "time"},
		MethodStubs: `func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	// TODO: implement
	return nil, false, nil
}

func (s *Store) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	// TODO: implement
	return nil, nil
}

func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	// TODO: implement
	return nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	// TODO: implement
	return nil
}

func (s *Store) DeletePattern(ctx context.Context, pattern string) error {
	// TODO: implement
	return nil
}

func (s *Store) Close() error {
	// TODO: implement
	return nil
}`,
	},

	"events": {
		InterfacePkg:  "infrastructure/events",
		InterfaceRef:  "events.Bus",
		StructName:    "Bus",
		ReceiverVar:   "b",
		CompliancePkg: "infrastructure/events/eventstest",
		ComplianceCall: "eventstest.RunSuite(t, {{var}})",
		DeferClose:    "{{var}}.Close(context.Background())",
		AdapterDir:    "infrastructure/events",
		SourceImports: []string{"context"},
		TestImports:   []string{"context"},
		MethodStubs: `func (b *Bus) Emit(ctx context.Context, event events.Event, opts ...events.EmitOption) error {
	// TODO: implement
	return nil
}

func (b *Bus) Subscribe(topic string, handler events.Handler) (events.Subscription, error) {
	// TODO: implement
	return nil, nil
}

func (b *Bus) Close(ctx context.Context) error {
	// TODO: implement
	return nil
}`,
	},

	"ratelimiter": {
		InterfacePkg:  "infrastructure/ratelimiter",
		InterfaceRef:  "ratelimiter.Storer",
		StructName:    "Store",
		ReceiverVar:   "s",
		CompliancePkg: "infrastructure/ratelimiter/ratelimitertest",
		ComplianceCall: "ratelimitertest.RunSuite(t, {{var}})",
		DeferClose:    "{{var}}.Close()",
		AdapterDir:    "infrastructure/ratelimiter",
		SourceImports: []string{"context"},
		MethodStubs: `func (s *Store) Allow(ctx context.Context, key string, limit ratelimiter.Limit) (ratelimiter.Result, error) {
	// TODO: implement
	return ratelimiter.Result{}, nil
}

func (s *Store) Reset(ctx context.Context, key string) error {
	// TODO: implement
	return nil
}

func (s *Store) Close() error {
	// TODO: implement
	return nil
}`,
	},

	"hasher": {
		InterfacePkg:  "infrastructure/cryptids",
		InterfaceRef:  "cryptids.PasswordHasher",
		StructName:    "Hasher",
		ReceiverVar:   "h",
		CompliancePkg: "infrastructure/cryptids/cryptidstest",
		ComplianceCall: "cryptidstest.RunHasherSuite(t, {{var}})",
		AdapterDir:    "infrastructure/cryptids",
		MethodStubs: `func (h *Hasher) Hash(password string) (string, error) {
	// TODO: implement
	return "", nil
}

func (h *Hasher) Compare(hash, password string) error {
	// TODO: implement
	return nil
}`,
	},

	"token": {
		InterfacePkg:  "infrastructure/cryptids",
		InterfaceRef:  "cryptids.TokenSigner",
		StructName:    "Signer",
		ReceiverVar:   "s",
		CompliancePkg: "infrastructure/cryptids/cryptidstest",
		ComplianceCall: "cryptidstest.RunSignerSuite(t, {{var}})",
		AdapterDir:    "infrastructure/cryptids",
		SourceImports: []string{"time"},
		MethodStubs: `func (s *Signer) Sign(claims map[string]any, expiresAt time.Time) (string, error) {
	// TODO: implement
	return "", nil
}

func (s *Signer) Verify(token string) (map[string]any, error) {
	// TODO: implement
	return nil, nil
}`,
	},

	"storage": {
		InterfacePkg:  "infrastructure/storage",
		InterfaceRef:  "storage.Client",
		StructName:    "Client",
		ReceiverVar:   "c",
		CompliancePkg: "infrastructure/storage/storagetest",
		ComplianceCall: "storagetest.RunSuite(t, {{var}})",
		AdapterDir:    "infrastructure/storage",
		SourceImports: []string{"context", "io"},
		MethodStubs: `func (c *Client) Upload(ctx context.Context, path string, reader io.Reader) error {
	// TODO: implement
	return nil
}

func (c *Client) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	// TODO: implement
	return nil, nil
}

func (c *Client) Delete(ctx context.Context, path string) error {
	// TODO: implement
	return nil
}

func (c *Client) Exists(ctx context.Context, path string) (bool, error) {
	// TODO: implement
	return false, nil
}

func (c *Client) List(ctx context.Context, prefix string) ([]string, error) {
	// TODO: implement
	return nil, nil
}

func (c *Client) DownloadRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	// TODO: implement
	return nil, nil
}

func (c *Client) GetObjectSize(ctx context.Context, path string) (int64, error) {
	// TODO: implement
	return 0, nil
}`,
	},

	"emailer": {
		InterfacePkg:  "infrastructure/communications/emailer",
		InterfaceRef:  "emailer.Client",
		StructName:    "Client",
		ReceiverVar:   "c",
		CompliancePkg: "infrastructure/communications/emailer/emailertest",
		ComplianceCall: "emailertest.RunSuite(t, {{var}})",
		AdapterDir:    "infrastructure/communications/emailer",
		SourceImports: []string{"context"},
		MethodStubs: `func (c *Client) Send(ctx context.Context, email emailer.Email) error {
	// TODO: implement
	return nil
}`,
	},
}

type adapterSpec struct {
	InterfacePkg   string
	InterfaceRef   string
	StructName     string
	ReceiverVar    string
	CompliancePkg  string
	ComplianceCall string
	DeferClose     string
	AdapterDir     string
	SourceImports  []string
	TestImports    []string
	MethodStubs    string
}
