package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gopernicus/gopernicus-cli/internal/generators"
	"github.com/gopernicus/gopernicus-cli/internal/project"
)

func runNewCase(_ context.Context, args []string) error {
	var nameArg string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && nameArg == "" {
			nameArg = a
		}
	}

	if nameArg == "" {
		return fmt.Errorf("case name required (e.g. tenantadmin)\n\nUsage: gopernicus new case <name>")
	}

	// Normalize: lowercase, no separators.
	name := strings.ToLower(strings.ReplaceAll(nameArg, "-", ""))
	kebab := generators.ToKebabCase(nameArg)
	bridgePkg := name + "bridge"

	root, err := project.MustFindRoot()
	if err != nil {
		return err
	}

	modulePath, err := project.ModulePath(root)
	if err != nil {
		return fmt.Errorf("reading module path: %w", err)
	}

	type scaffoldFile struct {
		dir      string // relative to project root
		filename string
		content  string
	}

	caseDir := filepath.Join("core", "cases", name)
	bridgeDir := filepath.Join("bridge", "cases", bridgePkg)

	files := []scaffoldFile{
		{
			dir:      caseDir,
			filename: "case.go",
			content: fmt.Sprintf(`// Package %s provides business logic for %s operations.
//
// This case orchestrates repositories, authorization, and events
// for operations that go beyond simple CRUD.
package %s

import (
	"context"
	"log/slog"

	"github.com/gopernicus/gopernicus/infrastructure/events"
)

// =============================================================================
// Dependencies
// =============================================================================

// TODO: Define dependency interfaces here. Accept interfaces, not concrete types.
// Example:
//   type EntityRepository interface {
//       Get(ctx context.Context, id string) (Entity, error)
//   }

// =============================================================================
// Case
// =============================================================================

// Case provides %s business logic.
type Case struct {
	log *slog.Logger
	bus events.Bus
	// TODO: Add repository and service dependencies.
}

// Option configures a Case.
type Option func(*Case)

// New creates a new %s Case.
func New(
	log *slog.Logger,
	bus events.Bus,
	// TODO: Add required dependencies as parameters.
	opts ...Option,
) *Case {
	c := &Case{
		log: log,
		bus: bus,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// =============================================================================
// Operations
// =============================================================================

// TODO: Add operations here. Each operation should have its own Input/Result types.
// Example:
//
//   type DoSomethingInput struct {
//       ResourceID string
//   }
//
//   type DoSomethingResult struct {
//       Success bool
//   }
//
//   func (c *Case) DoSomething(ctx context.Context, input DoSomethingInput) (DoSomethingResult, error) {
//       // orchestrate repos, auth, events
//       return DoSomethingResult{}, nil
//   }
`, name, kebab, name, kebab, kebab),
		},
		{
			dir:      caseDir,
			filename: "errors.go",
			content: fmt.Sprintf(`package %s

// Domain errors for the %s case.
// Wrap with sdk/errs sentinels for proper HTTP status mapping in the bridge layer.
//
// Example:
//   var (
//       ErrNotFound = fmt.Errorf("not found: %%w", errs.ErrNotFound)
//       ErrConflict = fmt.Errorf("conflict: %%w", errs.ErrConflict)
//   )
`, name, kebab),
		},
		{
			dir:      caseDir,
			filename: "events.go",
			content: fmt.Sprintf(`package %s

// Domain events emitted by the %s case.
// Subscribe to these in the bridge layer's subscribers.go.
//
// Example:
//   type SomethingHappenedEvent struct {
//       events.BaseEvent
//       ResourceID string `+"`"+`json:"resource_id"`+"`"+`
//   }
//
//   func (e SomethingHappenedEvent) Type() string { return "%s.something_happened" }
`, name, kebab, kebab),
		},
		{
			dir:      bridgeDir,
			filename: "bridge.go",
			content: fmt.Sprintf(`// Package %s provides HTTP handlers for %s operations.
package %s

import (
	"log/slog"

	"github.com/gopernicus/gopernicus/bridge/transit/httpmid"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
	%s "%s/core/cases/%s"
)

// Bridge is the HTTP handler bridge for %s operations.
type Bridge struct {
	log         *slog.Logger
	useCase     *%s.Case
	rateLimiter *ratelimiter.RateLimiter
	jsonErrors  httpmid.ErrorRenderer
}

// BridgeOption configures optional Bridge dependencies.
type BridgeOption func(*Bridge)

// WithJSONErrorRenderer overrides the default JSON error renderer.
func WithJSONErrorRenderer(r httpmid.ErrorRenderer) BridgeOption {
	return func(b *Bridge) { b.jsonErrors = r }
}

// New creates a new %s bridge.
func New(
	log *slog.Logger,
	useCase *%s.Case,
	rateLimiter *ratelimiter.RateLimiter,
	opts ...BridgeOption,
) *Bridge {
	b := &Bridge{
		log:         log,
		useCase:     useCase,
		rateLimiter: rateLimiter,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}
`, bridgePkg, kebab, bridgePkg, name, modulePath, name, kebab, name, kebab, name),
		},
		{
			dir:      bridgeDir,
			filename: "http.go",
			content: fmt.Sprintf(`package %s

import (
	"github.com/gopernicus/gopernicus/bridge/transit/httpmid"
	"github.com/gopernicus/gopernicus/sdk/web"
)

// AddHttpRoutes registers HTTP routes for %s operations.
// Routes are registered under /%s/ within the provided group.
//
// Expected mount point: api.Group("/cases")
// Resulting paths: /api/v1/cases/%s/...
func (b *Bridge) AddHttpRoutes(group *web.RouteGroup) {
	g := group.Group("/%s")
	_ = g
	_ = httpmid.RateLimit // available for route middleware

	// TODO: Register routes here. Example:
	//   g.POST("/some-action", b.httpSomeAction,
	//       httpmid.Authenticate(b.authenticator, b.log, b.jsonErrors),
	//       httpmid.RateLimit(b.rateLimiter, b.log),
	//   )
}
`, bridgePkg, kebab, kebab, kebab, kebab),
		},
		{
			dir:      bridgeDir,
			filename: "model.go",
			content: fmt.Sprintf(`package %s

// Request and response types for %s HTTP handlers.
//
// Example:
//   type DoSomethingRequest struct {
//       Name string `+"`"+`json:"name"`+"`"+`
//   }
//
//   func (r *DoSomethingRequest) Validate() error {
//       var errs validation.Errors
//       errs.Add(validation.Required("name", r.Name))
//       return errs.Err()
//   }
//
//   type DoSomethingResponse struct {
//       ID string `+"`"+`json:"id"`+"`"+`
//   }
`, bridgePkg, kebab),
		},
	}

	for _, f := range files {
		absDir := filepath.Join(root, f.dir)
		if err := os.MkdirAll(absDir, 0755); err != nil {
			return fmt.Errorf("creating %s: %w", f.dir, err)
		}

		absPath := filepath.Join(absDir, f.filename)
		if fileExists(absPath) {
			fmt.Printf("  skip  %s/%s (already exists)\n", f.dir, f.filename)
			continue
		}

		if err := os.WriteFile(absPath, []byte(f.content), 0644); err != nil {
			return fmt.Errorf("writing %s/%s: %w", f.dir, f.filename, err)
		}
		fmt.Printf("  create %s/%s\n", f.dir, f.filename)
	}

	fmt.Printf("\nCase %q scaffolded.\n", name)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Add dependencies to core/cases/%s/case.go\n", name)
	fmt.Printf("  2. Implement operations in case.go\n")
	fmt.Printf("  3. Add routes in bridge/cases/%s/http.go\n", bridgePkg)
	fmt.Printf("  4. Wire in app/server/config/server.go:\n")
	fmt.Printf("       cases := api.Group(\"/cases\")\n")
	fmt.Printf("       %sBridge.AddHttpRoutes(cases)\n", name)

	return nil
}
