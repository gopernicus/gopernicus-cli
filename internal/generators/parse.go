package generators

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Parse reads and parses a queries.sql file.
func Parse(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return ParseString(string(data))
}

// ParseString parses the contents of a queries.sql file.
func ParseString(input string) (*File, error) {
	f := &File{
		Database: "primary",
	}

	lines := strings.Split(input, "\n")
	var (
		inQuery           bool
		currentName       string
		annotations       map[string]string
		filterAnnotations map[string]string // @filter:name -> spec
		typeAnnotations   map[string]string // @type:param_name -> go_type
		sqlLines          []string
		lastAnnotationKey string            // tracks last annotation for continuation lines
	)

	// appendContinuation appends a pipe continuation to the last annotation value.
	appendContinuation := func(continuation string) {
		if inQuery && annotations != nil {
			if v, ok := annotations[lastAnnotationKey]; ok {
				annotations[lastAnnotationKey] = v + " " + continuation
			}
		} else if !inQuery && f.FileAnnotations != nil {
			if v, ok := f.FileAnnotations[lastAnnotationKey]; ok {
				f.FileAnnotations[lastAnnotationKey] = v + " " + continuation
			}
		}
	}

	flush := func() error {
		if currentName == "" {
			return nil
		}
		sql := strings.TrimSpace(strings.Join(sqlLines, "\n"))
		if sql == "" {
			return fmt.Errorf("query %q has no SQL", currentName)
		}
		sql = strings.TrimRight(sql, ";")
		sql = strings.TrimSpace(sql)

		// Build named filters map.
		filters := make(map[string]string)
		for name, spec := range filterAnnotations {
			filters[name] = spec
		}

		// Build type hints map.
		typeHints := make(map[string]string)
		for param, goType := range typeAnnotations {
			typeHints[param] = goType
		}

		// Order/limit/search from annotations.
		orderSpec := ""
		if v, ok := annotations["order"]; ok {
			orderSpec = v
		}
		limitSpec := ""
		if v, ok := annotations["max"]; ok {
			limitSpec = v
		}
		searchSpec := ""
		if v, ok := annotations["search"]; ok {
			searchSpec = v
		}
		cacheSpec := ""
		if v, ok := annotations["cache"]; ok {
			cacheSpec = v
		}
		eventType := ""
		if v, ok := annotations["event"]; ok {
			eventType = v
		}
		scanOverride := annotations["scan"]

		// Detect dynamic placeholders in SQL.
		hasFilters := len(filters) > 0
		hasSearch := strings.Contains(sql, "$search")

		qb := QueryBlock{
			Name:           strings.TrimSpace(currentName),
			Annotations:    annotations,
			SQL:            sql,
			Type:           detectQueryType(sql),
			Params:         extractParams(sql),
			Filters:        filters,
			HasFilters:     hasFilters,
			HasSearch:      hasSearch,
			HasFields:      strings.Contains(sql, "$fields"),
			HasValues:      strings.Contains(sql, "$values"),
			HasOrder:       strings.Contains(sql, "$order"),
			HasLimit:       strings.Contains(sql, "$limit"),
			ReturnsRows:    isReturning(sql),
			OrderSpec:      orderSpec,
			LimitSpec:      limitSpec,
			SearchSpec:     searchSpec,
			CacheSpec:      cacheSpec,
			EventType:      eventType,
			TypeHints:       typeHints,
			ScanOverride:   scanOverride,
		}
		f.Queries = append(f.Queries, qb)
		currentName = ""
		annotations = nil
		filterAnnotations = nil
		typeAnnotations = nil
		sqlLines = nil
		return nil
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "--") {
			body := strings.TrimSpace(strings.TrimPrefix(trimmed, "--"))

			// Continuation line: starts with | (appends to previous annotation).
			if strings.HasPrefix(body, "|") && lastAnnotationKey != "" {
				appendContinuation(strings.TrimSpace(body))
				continue
			}

			if key, name, val, ok := parseAnnotation(body); ok {
				// Track full annotation key for continuation lines.
				if name != "" {
					lastAnnotationKey = key + ":" + name
				} else {
					lastAnnotationKey = key
				}

				if key == "func" {
					if err := flush(); err != nil {
						return nil, fmt.Errorf("line %d: %w", i+1, err)
					}
					inQuery = true
					currentName = val
					annotations = nil
					filterAnnotations = nil
					typeAnnotations = nil
					sqlLines = nil
					continue
				}

				if !inQuery {
					switch key {
					case "database":
						f.Database = val
					default:
						if f.FileAnnotations == nil {
							f.FileAnnotations = make(map[string]string)
						}
						f.FileAnnotations[key] = val
					}
				} else {
					// @type:param_name go_type — explicit param type override.
					if key == "type" && name != "" {
						if typeAnnotations == nil {
							typeAnnotations = make(map[string]string)
						}
						typeAnnotations[name] = val
						continue
					}
					// Named annotations go to filter map.
					if key == "filter" && name != "" {
						if filterAnnotations == nil {
							filterAnnotations = make(map[string]string)
						}
						filterAnnotations[name] = val
					} else {
						if annotations == nil {
							annotations = make(map[string]string)
						}
						annotations[key] = val
					}
				}
				continue
			}

			// Plain comment line — skip.
			continue
		}

		if !inQuery {
			return nil, fmt.Errorf("line %d: SQL outside of a query block: %s", i+1, trimmed)
		}
		sqlLines = append(sqlLines, trimmed)

		if strings.HasSuffix(trimmed, ";") {
			if err := flush(); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			inQuery = false
		}
	}

	if err := flush(); err != nil {
		return nil, err
	}

	return f, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// parseAnnotation parses annotation bodies after "--".
//
//	Standard:  "@func: ListUsers"      → key="func",   name="",           val="ListUsers"
//	Named:     "@filter:conditions *"  → key="filter",  name="conditions", val="*"
//	Named:     "@http:json GET /foo"   → key="http",    name="json",       val="GET /foo"
//	Standard:  "@order: *,-foo"        → key="order",   name="",           val="*,-foo"
func parseAnnotation(body string) (key, name, val string, ok bool) {
	if !strings.HasPrefix(body, "@") {
		return "", "", "", false
	}
	rest := body[1:] // strip @
	idx := strings.IndexByte(rest, ':')
	if idx < 0 {
		// Bare annotation with no colon or value (e.g. @authenticated).
		key = strings.TrimSpace(rest)
		if key == "" {
			return "", "", "", false
		}
		return key, "", "", true
	}
	key = strings.TrimSpace(rest[:idx])
	if key == "" {
		return "", "", "", false
	}

	after := rest[idx+1:]

	// Named variant: no space immediately after ':'
	// e.g. @filter:conditions *,-record_state
	// after = "conditions *,-record_state"
	if len(after) > 0 && after[0] != ' ' {
		spaceIdx := strings.IndexByte(after, ' ')
		if spaceIdx < 0 {
			// @filter:conditions (no value — name only)
			name = after
			val = ""
		} else {
			name = after[:spaceIdx]
			val = strings.TrimSpace(after[spaceIdx+1:])
		}
		return key, name, val, true
	}

	// Standard: space after colon
	val = strings.TrimSpace(after)
	return key, "", val, true
}

func detectQueryType(sql string) QueryType {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	// CTEs start with WITH but the main statement determines the type.
	if strings.HasPrefix(upper, "WITH") {
		main := findMainStatement(upper)
		upper = main
	}
	switch {
	case strings.HasPrefix(upper, "SELECT"):
		return QuerySelect
	case strings.HasPrefix(upper, "INSERT"):
		return QueryInsert
	case strings.HasPrefix(upper, "UPDATE"):
		return QueryUpdate
	case strings.HasPrefix(upper, "DELETE"):
		return QueryDelete
	default:
		return QuerySelect
	}
}

// findMainStatement returns the SQL text after a CTE's WITH clause(s).
func findMainStatement(upper string) string {
	depth := 0
	for i := 0; i < len(upper); i++ {
		switch upper[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth > 0 {
			continue
		}
		rest := upper[i:]
		for _, kw := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			if strings.HasPrefix(rest, kw) {
				if i == 0 {
					continue
				}
				prev := upper[i-1]
				if prev == ' ' || prev == '\n' || prev == '\t' || prev == ')' {
					return rest
				}
			}
		}
	}
	return upper
}

var paramRegex = regexp.MustCompile(`@([a-zA-Z_][a-zA-Z0-9_]*)`)

func extractParams(sql string) []string {
	var sqlOnly []string
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "--") {
			sqlOnly = append(sqlOnly, line)
		}
	}
	cleaned := strings.Join(sqlOnly, "\n")

	matches := paramRegex.FindAllStringSubmatch(cleaned, -1)
	seen := make(map[string]bool)
	var params []string
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			params = append(params, name)
		}
	}
	return params
}

func isReturning(sql string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH") {
		return true
	}
	return strings.Contains(upper, "RETURNING")
}

func parseSearchAnnotation(spec string) (searchType, columns string) {
	spec = strings.TrimSpace(spec)

	parenIdx := strings.IndexByte(spec, '(')
	if parenIdx > 0 && strings.HasSuffix(spec, ")") {
		searchType = strings.TrimSpace(spec[:parenIdx])
		columns = strings.TrimSpace(spec[parenIdx+1 : len(spec)-1])
		return searchType, columns
	}

	return "ilike", spec
}
