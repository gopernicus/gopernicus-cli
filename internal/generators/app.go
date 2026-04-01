package generators

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// AppScaffoldData holds the template data for generating app bootstrap files.
type AppScaffoldData struct {
	// ProjectName is the directory/project name (e.g., "myapp").
	ProjectName string

	// ModulePath is the Go module path (e.g., "github.com/acme/myapp").
	ModulePath string

	// AppNameUpper is the env-var prefix in UPPER_CASE (e.g., "MYAPP").
	AppNameUpper string

	// Feature flags — control what wiring code is generated in server.go.
	HasAuthentication bool
	HasAuthorization  bool
	HasTenancy        bool

	// HasOutbox enables outbox adapter wiring in main.go.
	// When true, the generated main.go wraps the event bus with the outbox
	// adapter and bootstraps a WorkerPool for event processing.
	HasOutbox bool

	// Infrastructure flags — determine which adapter imports and config helpers
	// are generated. These reflect the user's TUI selections at init time and
	// control which Go packages appear in go.mod (once adapters are split out).
	HasRedis        bool // Redis client wired; enables redis cache backend option
	HasRedisStreams  bool // Redis Streams event bus backend (implies HasRedis)
	HasStorageDisk  bool // Local disk storage adapter
	HasStorageGCS   bool // Google Cloud Storage adapter
	HasStorageS3    bool // AWS S3 / compatible object storage adapter
	HasSendGrid     bool // SendGrid email delivery adapter
	HasTelemetry    bool // Telemetry stack (Jaeger for now; Grafana/Prometheus later)

	// Derived flags — computed from the above, not set directly.
	HasStorage bool // true if any storage adapter selected (Disk, GCS, or S3)
}

// GenerateAppScaffold creates the app bootstrap files (main.go, server.go, .env.example)
// inside the given project root directory.
//
// These are bootstrap files — they are only created if they don't already exist.
func GenerateAppScaffold(root string, data AppScaffoldData) error {
	// templatedFiles are rendered through text/template before writing.
	templatedFiles := []struct {
		relPath  string
		tmplName string
		tmplSrc  string
	}{
		{"app/server/main.go", "main", mainTemplate},
		{"app/server/config/server.go", "server", serverTemplate},
		{"app/server/emails/emails.go", "emails", emailsTemplate},
		{".env.example", "envExample", envExampleTemplate},
		{".env", "env", envExampleTemplate}, // same content — no real creds, safe to commit as starting point
		{"workshop/dev/docker-compose.yml", "dockerCompose", dockerComposeTemplate},
		{"workshop/docker/dockerfile." + data.ProjectName, "dockerfile", dockerfileTemplate},
		{"workshop/documentation/README.md", "docsREADME", documentationREADMETemplate},
		{"workshop/documentation/architecture/overview.md", "docsArchOverview", documentationArchOverviewTemplate},
		{"workshop/documentation/deployment/docker.md", "docsDeployDocker", documentationDeployDockerTemplate},
		{"Makefile", "makefile", makefileTemplate},
	}

	// rawFiles are written as-is — their content must not be processed through
	// text/template because they contain emailer template syntax ({{define}}, etc.).
	rawFiles := []struct {
		relPath string
		content string
	}{
		{".air.toml", airTomlRaw},
		{"app/server/emails/layouts/transactional.html", emailLayoutHTMLRaw},
		{"app/server/emails/layouts/transactional.txt", emailLayoutTXTRaw},
	}
	if data.HasAuthentication {
		rawFiles = append(rawFiles,
			struct {
				relPath string
				content string
			}{"app/server/emails/templates/authentication/verification.html", authVerificationHTMLRaw},
			struct {
				relPath string
				content string
			}{"app/server/emails/templates/authentication/verification.txt", authVerificationTXTRaw},
			struct {
				relPath string
				content string
			}{"app/server/emails/templates/authentication/password_reset.html", authPasswordResetHTMLRaw},
			struct {
				relPath string
				content string
			}{"app/server/emails/templates/authentication/password_reset.txt", authPasswordResetTXTRaw},
			struct {
				relPath string
				content string
			}{"app/server/emails/templates/authentication/oauth_link_verification.html", authOAuthLinkHTMLRaw},
			struct {
				relPath string
				content string
			}{"app/server/emails/templates/authentication/oauth_link_verification.txt", authOAuthLinkTXTRaw},
		)
	}

	for _, f := range templatedFiles {
		target := filepath.Join(root, f.relPath)

		// Bootstrap: skip if file already exists.
		if _, err := os.Stat(target); err == nil {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", f.relPath, err)
		}

		tmpl, err := template.New(f.tmplName).Parse(f.tmplSrc)
		if err != nil {
			return fmt.Errorf("parsing template %s: %w", f.tmplName, err)
		}

		out, err := os.Create(target)
		if err != nil {
			return fmt.Errorf("creating %s: %w", f.relPath, err)
		}

		if err := tmpl.Execute(out, data); err != nil {
			out.Close()
			return fmt.Errorf("rendering %s: %w", f.relPath, err)
		}

		if err := out.Close(); err != nil {
			return fmt.Errorf("closing %s: %w", f.relPath, err)
		}
	}

	for _, f := range rawFiles {
		target := filepath.Join(root, f.relPath)

		if _, err := os.Stat(target); err == nil {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", f.relPath, err)
		}

		if err := os.WriteFile(target, []byte(f.content), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", f.relPath, err)
		}
	}

	return nil
}

// AppNameFromProject derives an UPPER_CASE env prefix from a project name.
// e.g., "my-app" → "MY_APP", "myapp" → "MYAPP".
func AppNameFromProject(projectName string) string {
	return strings.ToUpper(strings.ReplaceAll(projectName, "-", "_"))
}
