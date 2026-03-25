package generators

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// BridgeYML is the root structure of a bridge.yml file.
type BridgeYML struct {
	// Entity is the PascalCase entity name, e.g. "Question".
	Entity string `yaml:"entity"`

	// Repo is the path to the repository package relative to core/repositories/,
	// e.g. "questions/questions".
	Repo string `yaml:"repo"`

	// Domain is the domain name, e.g. "questions".
	Domain string `yaml:"domain"`

	// Auth schema — authorization is a bridge concern.
	AuthRelations   []string `yaml:"auth_relations,omitempty"`
	AuthPermissions []string `yaml:"auth_permissions,omitempty"`

	// Routes defines the HTTP routes to generate.
	Routes []BridgeYMLRoute `yaml:"routes"`
}

// BridgeYMLRoute defines a single HTTP route.
type BridgeYMLRoute struct {
	// Func is the repository function name this route maps to, e.g. "List", "Get", "Create".
	Func string `yaml:"func"`

	// Path is the URL path with {param} placeholders.
	Path string `yaml:"path"`

	// Method overrides the auto-derived HTTP method.
	Method string `yaml:"method,omitempty"`

	// WithPermissions indicates the response should include permissions.
	WithPermissions bool `yaml:"with_permissions,omitempty"`

	// ParamsToInput lists path parameter names that should be extracted from the
	// URL and set on the repo input struct. Used for parent-scoped creates where
	// the parent FK comes from the URL, not the request body.
	// e.g. ["tenant_id"] on a POST /tenants/{tenant_id}/questions route.
	ParamsToInput []string `yaml:"params_to_input,omitempty"`

	// AuthCreate specifies authorization relationships to create.
	// Compact format: "resource_type:{resource_id}#relation@subject_type:{subject_id}"
	AuthCreate []string `yaml:"auth_create,omitempty"`

	// Middleware is an ordered list of middleware to apply to this route.
	// Each entry is either:
	//   - A known middleware: "rate_limit", {authenticate: "user"}, {authorize: {...}}, {max_body_size: 1048576}
	//   - A raw Go expression string for custom middleware
	Middleware []MiddlewareEntry `yaml:"middleware"`
}

// MiddlewareEntry represents a single middleware in the chain.
// It can be a known type (authenticate, authorize, rate_limit, max_body_size, unique_to_id)
// or a raw Go expression for custom middleware.
type MiddlewareEntry struct {
	// Known middleware types (only one is set per entry):
	Authenticate string           `yaml:"-"` // "user", "service_account", "user_session", "any"
	Authorize    *AuthorizeEntry  `yaml:"-"` // authorization config
	UniqueToID   *UniqueToIDEntry `yaml:"-"` // resolve unique value to ID
	RateLimit    bool             `yaml:"-"` // true if rate_limit
	MaxBodySize  int64            `yaml:"-"` // body size in bytes
	Raw          string           `yaml:"-"` // raw Go expression for custom middleware
}

// UniqueToIDEntry resolves a unique value (slug, email, etc.) to a resource ID.
type UniqueToIDEntry struct {
	// Resolver is the repo function name to call, e.g. "GetIDBySlug", "GetByEmail".
	Resolver string `yaml:"resolver"`
	// Param is the path param holding the lookup value, e.g. "slug", "email".
	Param string `yaml:"param"`
	// TargetParam is the param name to inject the resolved ID as, e.g. "tenant_id".
	TargetParam string `yaml:"target_param"`
	// IDField is the Go struct field name on the result that holds the ID, e.g. "TenantID".
	IDField string `yaml:"id_field"`
}

// AuthorizeEntry configures authorization middleware.
type AuthorizeEntry struct {
	Pattern    string `yaml:"pattern,omitempty"`    // "prefilter", "postfilter", or "" (defaults to "check")
	Permission string `yaml:"permission"`
	Param      string `yaml:"param,omitempty"`      // path param for check
	Entity     string `yaml:"entity,omitempty"`      // override resource type (default: bridge entity)
	Subject    string `yaml:"subject,omitempty"`     // explicit subject for prefilter
}

// UnmarshalYAML handles the polymorphic middleware entry format.
// Supports: "rate_limit", {authenticate: "user"}, {authorize: {...}}, {max_body_size: N}, "raw go expr"
func (m *MiddlewareEntry) UnmarshalYAML(value *yaml.Node) error {
	// Case 1: bare string — "rate_limit" or raw Go expression
	if value.Kind == yaml.ScalarNode {
		s := value.Value
		if s == "rate_limit" {
			m.RateLimit = true
			return nil
		}
		// Anything else is a raw Go expression.
		m.Raw = s
		return nil
	}

	// Case 2: map with one key
	if value.Kind == yaml.MappingNode {
		if len(value.Content) < 2 {
			return fmt.Errorf("middleware entry: empty map")
		}

		key := value.Content[0].Value
		val := value.Content[1]

		switch key {
		case "authenticate":
			m.Authenticate = val.Value
			return nil

		case "authorize":
			var auth AuthorizeEntry
			if err := val.Decode(&auth); err != nil {
				return fmt.Errorf("middleware authorize: %w", err)
			}
			m.Authorize = &auth
			return nil

		case "unique_to_id":
			var utid UniqueToIDEntry
			if err := val.Decode(&utid); err != nil {
				return fmt.Errorf("middleware unique_to_id: %w", err)
			}
			m.UniqueToID = &utid
			return nil

		case "max_body_size":
			var size int64
			if err := val.Decode(&size); err != nil {
				return fmt.Errorf("middleware max_body_size: %w", err)
			}
			m.MaxBodySize = size
			return nil

		case "rate_limit":
			m.RateLimit = true
			return nil

		default:
			return fmt.Errorf("unknown middleware %q", key)
		}
	}

	return fmt.Errorf("middleware entry must be a string or map, got %v", value.Kind)
}

// ParseBridgeYML reads and parses a bridge.yml file.
func ParseBridgeYML(path string) (*BridgeYML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bridge.yml: %w", err)
	}
	return ParseBridgeYMLBytes(data)
}

// ParseBridgeYMLBytes parses bridge.yml content from bytes.
func ParseBridgeYMLBytes(data []byte) (*BridgeYML, error) {
	var yml BridgeYML
	if err := yaml.Unmarshal(data, &yml); err != nil {
		return nil, fmt.Errorf("parse bridge.yml: %w", err)
	}

	if err := validateBridgeYML(&yml); err != nil {
		return nil, err
	}

	return &yml, nil
}

func validateBridgeYML(yml *BridgeYML) error {
	if yml.Entity == "" {
		return fmt.Errorf("bridge.yml: entity is required")
	}
	if yml.Repo == "" {
		return fmt.Errorf("bridge.yml: repo is required")
	}

	for i, route := range yml.Routes {
		if route.Func == "" {
			return fmt.Errorf("bridge.yml: routes[%d].func is required", i)
		}
		if route.Path == "" {
			return fmt.Errorf("bridge.yml: routes[%d].path is required", i)
		}

		if route.Method != "" {
			method := strings.ToUpper(route.Method)
			switch method {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
			default:
				return fmt.Errorf("bridge.yml: routes[%d].method %q is not a valid HTTP method", i, route.Method)
			}
		}

		// Validate middleware entries.
		for j, mw := range route.Middleware {
			if mw.Authenticate != "" {
				switch mw.Authenticate {
				case "user", "service_account", "user_session", "any":
				default:
					return fmt.Errorf("bridge.yml: routes[%d].middleware[%d] authenticate %q must be user, service_account, user_session, or any", i, j, mw.Authenticate)
				}
			}
			if mw.Authorize != nil {
				switch mw.Authorize.Pattern {
				case "", "prefilter", "postfilter", "check":
				default:
					return fmt.Errorf("bridge.yml: routes[%d].middleware[%d] authorize pattern %q invalid", i, j, mw.Authorize.Pattern)
				}
				if mw.Authorize.Permission == "" {
					return fmt.Errorf("bridge.yml: routes[%d].middleware[%d] authorize permission is required", i, j)
				}
				pattern := mw.Authorize.Pattern
				if pattern == "" {
					pattern = "check"
				}
				if pattern == "check" && mw.Authorize.Param == "" {
					return fmt.Errorf("bridge.yml: routes[%d].middleware[%d] authorize param is required for check", i, j)
				}
			}
			if mw.UniqueToID != nil {
				if mw.UniqueToID.Resolver == "" {
					return fmt.Errorf("bridge.yml: routes[%d].middleware[%d] unique_to_id resolver is required", i, j)
				}
				if mw.UniqueToID.Param == "" {
					return fmt.Errorf("bridge.yml: routes[%d].middleware[%d] unique_to_id param is required", i, j)
				}
				if mw.UniqueToID.TargetParam == "" {
					return fmt.Errorf("bridge.yml: routes[%d].middleware[%d] unique_to_id target_param is required", i, j)
				}
				if mw.UniqueToID.IDField == "" {
					return fmt.Errorf("bridge.yml: routes[%d].middleware[%d] unique_to_id id_field is required", i, j)
				}
			}
		}
	}

	return nil
}

// deriveHTTPMethod infers the HTTP method from the query category.
func deriveHTTPMethod(category string) string {
	switch category {
	case "list", "scan_one", "scan_one_custom", "scan_many":
		return "GET"
	case "create":
		return "POST"
	case "update", "update_returning":
		return "PUT"
	case "exec":
		return "DELETE"
	default:
		return "GET"
	}
}

// BridgeYMLToBridgeRoutes converts parsed bridge.yml routes into BridgeRoute
// structs for template rendering.
func BridgeYMLToBridgeRoutes(yml *BridgeYML, resolved *ResolvedFile) ([]BridgeRoute, error) {
	var routes []BridgeRoute

	for _, yr := range yml.Routes {
		rq, ok := findResolvedQuery(resolved, yr.Func)
		if !ok {
			return nil, fmt.Errorf("bridge.yml: route func %q not found in queries.sql", yr.Func)
		}

		category := categorizeQuery(rq)
		handlerName := "http" + yr.Func

		method := deriveHTTPMethod(category)
		if yr.Method != "" {
			method = strings.ToUpper(yr.Method)
		}

		br := BridgeRoute{
			Method:          method,
			Path:            yr.Path,
			FuncName:        yr.Func,
			HandlerName:     handlerName,
			Category:        category,
			HasFilters:      rq.HasFilters,
			HasOrder:        rq.HasOrder,
			MaxLimit:        rq.MaxLimit,
			PathParams:      extractPathParams(yr.Path, rq),
			WithPermissions: yr.WithPermissions,
		}

		if rq.HasFilters {
			br.FilterTypeName = "Filter" + rq.FuncName
		}

		switch category {
		case "create", "update", "update_returning":
			br.HasBody = true
		}

		// Extract authorize spec from middleware for handler-level logic
		// (prefilter/postfilter patterns need handler code, not just middleware).
		for _, mw := range yr.Middleware {
			if mw.Authorize != nil {
				pattern := mw.Authorize.Pattern
				if pattern == "" {
					pattern = "check"
				}
				br.Authorize = &AuthorizeSpec{
					Pattern:    pattern,
					Permission: mw.Authorize.Permission,
					Param:      mw.Authorize.Param,
					SubjectRef: mw.Authorize.Subject,
				}
			}
			if mw.Authenticate != "" {
				br.Authenticated = mw.Authenticate
			}
			if mw.MaxBodySize > 0 {
				br.MaxBodySize = mw.MaxBodySize
			}
		}

		// Resolve params_to_input — match names against extracted path params.
		for _, paramName := range yr.ParamsToInput {
			found := false
			for _, p := range br.PathParams {
				if p.Name == paramName {
					br.ParamsToInput = append(br.ParamsToInput, p)
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("bridge.yml: route %s %s params_to_input %q not found in path params", method, yr.Path, paramName)
			}
		}

		// Store the raw middleware chain for addGeneratedRoutes rendering.
		br.MiddlewareChain = yr.Middleware

		// Convert auth create relationships.
		if len(yr.AuthCreate) > 0 {
			var createRels []AuthCreateRel
			for _, spec := range yr.AuthCreate {
				rel, err := parseCompactAuthRel(spec)
				if err != nil {
					return nil, fmt.Errorf("bridge.yml: route %s %s auth_create %q: %w", method, yr.Path, spec, err)
				}
				createRels = append(createRels, rel)
			}
			br.CreateRels = resolveBridgeCreateRels(createRels)
		}

		// Flag hard-delete routes for auth relationship cleanup.
		if category == "exec" && isDeleteFunc(yr.Func) {
			pkParam := resolved.PKColumn
			for _, p := range br.PathParams {
				if p.Name == pkParam {
					br.DeleteCleanup = true
					br.DeleteCleanupGoName = p.GoName
					break
				}
			}
			if !br.DeleteCleanup {
				return nil, fmt.Errorf("bridge.yml: delete route %s %s has no path parameter matching primary key %q", method, yr.Path, pkParam)
			}
		}

		routes = append(routes, br)
	}

	return routes, nil
}

func findResolvedQuery(resolved *ResolvedFile, funcName string) (ResolvedQuery, bool) {
	for _, rq := range resolved.Queries {
		if rq.FuncName == funcName {
			return rq, true
		}
	}
	return ResolvedQuery{}, false
}

// parseCompactAuthRel parses "resource_type:{resource_id}#relation@subject_type:{subject_id}"
func parseCompactAuthRel(spec string) (AuthCreateRel, error) {
	hashParts := strings.SplitN(spec, "#", 2)
	if len(hashParts) != 2 {
		return AuthCreateRel{}, fmt.Errorf("expected resource#relation@subject format")
	}

	resType, resID, err := splitTypeRef(hashParts[0])
	if err != nil {
		return AuthCreateRel{}, fmt.Errorf("resource %q: %w", hashParts[0], err)
	}

	atParts := strings.SplitN(hashParts[1], "@", 2)
	if len(atParts) != 2 {
		return AuthCreateRel{}, fmt.Errorf("expected relation@subject after #")
	}

	relation := atParts[0]

	subType, subID, err := splitTypeRef(atParts[1])
	if err != nil {
		subType = atParts[1]
		subID = ""
	}

	return AuthCreateRel{
		ResourceType: resType,
		ResourceID:   resID,
		Relation:     relation,
		SubjectType:  subType,
		SubjectID:    subID,
	}, nil
}

func splitTypeRef(ref string) (string, string, error) {
	if strings.HasPrefix(ref, "{") && !strings.Contains(ref, ":") {
		return ref, "", nil
	}

	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("expected type:id format, got %q", ref)
	}
	return parts[0], parts[1], nil
}
