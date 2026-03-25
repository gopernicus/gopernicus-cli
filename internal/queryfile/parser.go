package queryfile

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
		inQuery     bool // true once we've seen a @func annotation
		currentName string
		annotations map[string]string
		sqlLines    []string
	)

	flush := func() error {
		if currentName == "" {
			return nil
		}
		sql := strings.TrimSpace(strings.Join(sqlLines, "\n"))
		if sql == "" {
			return fmt.Errorf("query %q has no SQL", currentName)
		}
		// Strip trailing semicolon.
		sql = strings.TrimRight(sql, ";")
		sql = strings.TrimSpace(sql)

		// Extract inline specs from SQL comments before storing.
		cleaned, filterSpec, orderSpec, limitSpec := extractInlineSpecs(sql)

		// Extract search type from @search annotation if present.
		var searchType string
		if searchAnnotation, ok := annotations["search"]; ok {
			st, _ := parseSearchAnnotation(searchAnnotation)
			searchType = st
		}

		// Detect explicit SELECT/RETURNING columns.
		selectCols := extractSelectCols(cleaned)
		returnCols := extractReturningCols(cleaned)

		qb := QueryBlock{
			Name:        strings.TrimSpace(currentName),
			Annotations: annotations,
			SQL:         cleaned,
			Type:        detectQueryType(cleaned),
			Params:      extractParams(cleaned),
			HasFilters:  strings.Contains(cleaned, "$filters"),
			HasFields:   strings.Contains(cleaned, "$fields"),
			HasValues:   strings.Contains(cleaned, "$values"),
			HasOrder:    strings.Contains(cleaned, "$order"),
			HasLimit:    strings.Contains(cleaned, "$lim"),
			ReturnsRows: isReturning(cleaned),
			FilterSpec:  filterSpec,
			OrderSpec:   orderSpec,
			LimitSpec:   limitSpec,
			SelectCols:  selectCols,
			ReturnCols:  returnCols,
			SearchType:  searchType,
		}
		f.Queries = append(f.Queries, qb)
		currentName = ""
		annotations = nil
		sqlLines = nil
		return nil
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip blank lines.
		if trimmed == "" {
			continue
		}

		// Comment line.
		if strings.HasPrefix(trimmed, "--") {
			body := strings.TrimSpace(strings.TrimPrefix(trimmed, "--"))

			// Try to parse as an annotation: -- @key: value
			if key, val, ok := parseAnnotation(body); ok {
				if key == "func" {
					// @func starts a new query block.
					if err := flush(); err != nil {
						return nil, fmt.Errorf("line %d: %w", i+1, err)
					}
					inQuery = true
					currentName = val
					annotations = nil
					sqlLines = nil
					continue
				}

				if !inQuery {
					// File-level annotation.
					switch key {
					case "database":
						f.Database = val
					default:
						// Store as file-level annotation for future use
						// (@parent, @relations, @permissions).
						if f.FileAnnotations == nil {
							f.FileAnnotations = make(map[string]string)
						}
						f.FileAnnotations[key] = val
					}
				} else {
					// Query-level annotation.
					if annotations == nil {
						annotations = make(map[string]string)
					}
					annotations[key] = val
				}
				continue
			}

			// Non-annotation comment. If we're collecting SQL, trailing
			// comments after SQL lines are ignored. If we haven't started
			// a query block yet, just skip the comment.
			if inQuery && len(sqlLines) > 0 {
				continue
			}

			continue
		}

		// SQL line — must be inside a query block.
		if !inQuery {
			return nil, fmt.Errorf("line %d: SQL outside of a query block: %s", i+1, trimmed)
		}
		sqlLines = append(sqlLines, trimmed)

		// If the line ends with a semicolon, the query is complete.
		if strings.HasSuffix(trimmed, ";") {
			if err := flush(); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			inQuery = false
		}
	}

	// Flush any remaining query (file may not end with semicolon).
	if err := flush(); err != nil {
		return nil, err
	}

	return f, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// parseAnnotation tries to parse "-- @key: value" from the body after "--".
// Returns key, value, ok.
func parseAnnotation(body string) (string, string, bool) {
	if !strings.HasPrefix(body, "@") {
		return "", "", false
	}
	rest := body[1:] // strip @
	idx := strings.IndexByte(rest, ':')
	if idx < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(rest[:idx])
	val := strings.TrimSpace(rest[idx+1:])
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

// extractInlineSpecs scans SQL lines for inline comment specs attached to
// placeholder keywords. Recognized patterns:
//
//	$filters -- spec   → filterSpec
//	$order -- spec     → orderSpec
//	$lim -- default    → limitSpec
//
// Returns the cleaned SQL (inline specs stripped) and extracted values.
func extractInlineSpecs(sql string) (cleaned, filterSpec, orderSpec, limitSpec string) {
	var cleanedLines []string
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)

		// Look for " -- " inline comment.
		if idx := strings.Index(trimmed, " -- "); idx > 0 {
			sqlPart := strings.TrimSpace(trimmed[:idx])
			commentPart := strings.TrimSpace(trimmed[idx+4:])

			switch {
			case strings.Contains(sqlPart, "$filters"):
				filterSpec = commentPart
			case strings.Contains(sqlPart, "$order"):
				orderSpec = commentPart
			case strings.Contains(sqlPart, "$lim"):
				limitSpec = commentPart
			}
			// Keep only the SQL part.
			cleanedLines = append(cleanedLines, sqlPart)
		} else {
			cleanedLines = append(cleanedLines, trimmed)
		}
	}
	return strings.Join(cleanedLines, "\n"), filterSpec, orderSpec, limitSpec
}

// detectQueryType determines the SQL verb from the first keyword.
func detectQueryType(sql string) QueryType {
	upper := strings.ToUpper(strings.TrimSpace(sql))
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
		return QuerySelect // fallback
	}
}

// paramRegex matches @param_name in SQL.
var paramRegex = regexp.MustCompile(`@([a-zA-Z_][a-zA-Z0-9_]*)`)

// extractParams finds all @param_name references in SQL.
// Comment lines (starting with --) are stripped first to avoid matching annotations.
func extractParams(sql string) []string {
	// Remove comment lines to avoid matching annotations.
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

// isReturning returns true if the SQL is a SELECT or contains a RETURNING clause.
func isReturning(sql string) bool {
	upper := strings.ToUpper(sql)
	if strings.HasPrefix(strings.TrimSpace(upper), "SELECT") {
		return true
	}
	return strings.Contains(upper, "RETURNING")
}

// extractSelectCols returns the explicit column list from a SELECT clause.
// Returns "" if the query uses SELECT * (wildcard) or is not a SELECT.
//
// Examples:
//
//	"SELECT * FROM users ..."             → ""
//	"SELECT user_id, email FROM users"    → "user_id, email"
//	"INSERT INTO users ..."               → ""
func extractSelectCols(sql string) string {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if !strings.HasPrefix(upper, "SELECT") {
		return ""
	}

	// Find "SELECT " prefix (case-insensitive match, preserve original case).
	trimmed := strings.TrimSpace(sql)
	rest := strings.TrimSpace(trimmed[len("SELECT"):])

	// Wildcard — no explicit columns.
	if strings.HasPrefix(rest, "*") {
		return ""
	}

	// Find FROM keyword to delimit the column list.
	fromIdx := findKeyword(upper, "FROM")
	if fromIdx < 0 {
		return ""
	}

	// Extract between SELECT and FROM.
	selectEnd := len("SELECT")
	cols := strings.TrimSpace(trimmed[selectEnd:fromIdx])
	return cols
}

// extractReturningCols returns the explicit column list from a RETURNING clause.
// Returns "" if the query uses RETURNING * or has no RETURNING clause.
//
// Examples:
//
//	"INSERT ... RETURNING *"               → ""
//	"INSERT ... RETURNING user_id, email"  → "user_id, email"
//	"DELETE ... WHERE user_id = @user_id"  → ""
func extractReturningCols(sql string) string {
	upper := strings.ToUpper(sql)
	idx := findKeyword(upper, "RETURNING")
	if idx < 0 {
		return ""
	}

	rest := strings.TrimSpace(sql[idx+len("RETURNING"):])
	if rest == "" || strings.HasPrefix(rest, "*") {
		return ""
	}

	return rest
}

// findKeyword finds the position of a SQL keyword in an uppercased string,
// ensuring it appears as a whole word (preceded by whitespace or start of string,
// followed by whitespace).
func findKeyword(upper, keyword string) int {
	search := upper
	offset := 0
	for {
		idx := strings.Index(search, keyword)
		if idx < 0 {
			return -1
		}
		// Check word boundary before.
		if idx > 0 {
			prev := search[idx-1]
			if prev != ' ' && prev != '\t' && prev != '\n' {
				offset += idx + len(keyword)
				search = search[idx+len(keyword):]
				continue
			}
		}
		// Check word boundary after.
		after := idx + len(keyword)
		if after < len(search) {
			next := search[after]
			if next != ' ' && next != '\t' && next != '\n' {
				offset += idx + len(keyword)
				search = search[idx+len(keyword):]
				continue
			}
		}
		return offset + idx
	}
}

// parseSearchAnnotation splits a @search annotation value into its type and
// column list. Returns the search type and raw column string.
//
// Examples:
//
//	"ilike(email, name)"       → "ilike", "email, name"
//	"web_search(title, body)"  → "web_search", "title, body"
//	"tsvector(search_vector)"  → "tsvector", "search_vector"
//	"email, name"              → "ilike", "email, name"   (default)
func parseSearchAnnotation(spec string) (searchType, columns string) {
	spec = strings.TrimSpace(spec)

	parenIdx := strings.IndexByte(spec, '(')
	if parenIdx > 0 && strings.HasSuffix(spec, ")") {
		searchType = strings.TrimSpace(spec[:parenIdx])
		columns = strings.TrimSpace(spec[parenIdx+1 : len(spec)-1])
		return searchType, columns
	}

	// No parentheses — default to ilike with the whole spec as columns.
	return "ilike", spec
}
