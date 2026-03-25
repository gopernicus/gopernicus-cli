package generators

import (
	"fmt"
	"regexp"
	"strings"

	pgq "github.com/pganalyze/pg_query_go/v6"
	pg_query "github.com/wasilibs/go-pgquery"
)

// ─── public types ────────────────────────────────────────────────────────────

// ASTResult holds the parsed AST information for a single SQL query.
type ASTResult struct {
	QueryType  QueryType
	SelectCols []ASTColumn            // main SELECT columns (or RETURNING columns)
	ReturnCols []ASTColumn            // RETURNING clause columns (UPDATE/INSERT)
	CTEColumns map[string][]ASTColumn // CTE name → output columns
	AliasMap   map[string]string      // table alias → table/CTE name
	HasWhere   bool
}

// ASTColumn holds type information for a single output column from the AST.
type ASTColumn struct {
	Name         string // output name (alias or column name)
	SourceTable  string // table alias prefix (empty if none)
	SourceCol    string // original column name before AS
	NodeType     string // "column", "func", "sublink_exists", "sublink_expr", "coalesce", "const", "star", "case", "unknown"
	FuncName     string // for func nodes: "count", "max", etc.
	InferredType string // Go type from AST alone ("int64", "bool", "time.Time", "")
}

// ─── SQL preparation ─────────────────────────────────────────────────────────

var namedParamRe = regexp.MustCompile(`@([a-zA-Z_][a-zA-Z0-9_]*)`)

// PrepareForParse transforms annotated SQL into valid Postgres so pg_query can parse it.
// Steps:
//  1. Replace @param_name → $N (numbered positional params)
//  2. Replace $filterName and $search → TRUE
//  3. Strip lines containing ORDER BY $order and LIMIT $limit
//  4. Clean dangling WHERE TRUE AND TRUE → WHERE TRUE (valid SQL)
//  5. Return cleaned SQL
func PrepareForParse(sql string, filterNames []string) string {
	// Step 1: Replace @params with numbered positional params.
	paramIdx := 0
	paramMap := make(map[string]int)
	cleaned := namedParamRe.ReplaceAllStringFunc(sql, func(match string) string {
		name := match[1:] // strip @
		if idx, ok := paramMap[name]; ok {
			return fmt.Sprintf("$%d", idx)
		}
		paramIdx++
		paramMap[name] = paramIdx
		return fmt.Sprintf("$%d", paramIdx)
	})

	// Step 2: Replace $filterName and $search placeholders with TRUE.
	for _, name := range filterNames {
		cleaned = strings.ReplaceAll(cleaned, "$"+name, "TRUE")
	}
	cleaned = strings.ReplaceAll(cleaned, "$search", "TRUE")

	// Step 3: Strip ORDER BY $order and LIMIT $limit clauses.
	orderRe := regexp.MustCompile(`(?i)\s*ORDER\s+BY\s+\$order\b[^\n]*`)
	cleaned = orderRe.ReplaceAllString(cleaned, "")
	limitRe := regexp.MustCompile(`(?i)\s*LIMIT\s+\$limit\b[^\n]*`)
	cleaned = limitRe.ReplaceAllString(cleaned, "")

	// Step 4: Replace $fields and $values placeholders (INSERT/UPDATE).
	// These can't produce valid SQL regardless, so replace with placeholder expressions.
	if strings.Contains(cleaned, "$fields") || strings.Contains(cleaned, "$values") {
		cleaned = strings.ReplaceAll(cleaned, "($fields)", "(col1)")
		cleaned = strings.ReplaceAll(cleaned, "($values)", "($1)")
		cleaned = strings.ReplaceAll(cleaned, "$fields", "col1 = $1")
	}

	// Step 5: Clean up trailing WHERE/AND/OR artifacts.
	cleaned = cleanTrailingClauses(cleaned)

	return cleaned
}

// cleanTrailingClauses removes trailing WHERE with no conditions and
// dangling AND/OR at the end of WHERE clauses.
func cleanTrailingClauses(sql string) string {
	lines := strings.Split(sql, "\n")
	var result []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)

		// Skip empty WHERE lines.
		if upper == "WHERE" || upper == "WHERE;" {
			continue
		}

		result = append(result, line)
	}

	joined := strings.Join(result, "\n")

	// Remove trailing AND/OR before ORDER BY, LIMIT, GROUP BY, HAVING, or end.
	trailingRe := regexp.MustCompile(`(?i)\s+(AND|OR)\s*$`)
	joined = trailingRe.ReplaceAllString(joined, "")

	return joined
}

// ─── main entry point ────────────────────────────────────────────────────────

// ParseSQL parses a prepared SQL statement and extracts column information.
// The SQL should be preprocessed with PrepareForParse first.
func ParseSQL(sql string) (*ASTResult, error) {
	result, err := pg_query.Parse(sql)
	if err != nil {
		return nil, fmt.Errorf("pg_query parse: %w", err)
	}

	if len(result.Stmts) == 0 {
		return nil, fmt.Errorf("no statements found")
	}

	stmt := result.Stmts[0].Stmt
	ast := &ASTResult{
		CTEColumns: make(map[string][]ASTColumn),
		AliasMap:   make(map[string]string),
	}

	switch {
	case stmt.GetSelectStmt() != nil:
		ast.QueryType = QuerySelect
		sel := stmt.GetSelectStmt()
		ast.parseSelect(sel)

	case stmt.GetInsertStmt() != nil:
		ast.QueryType = QueryInsert
		ins := stmt.GetInsertStmt()
		if len(ins.GetReturningList()) > 0 {
			ast.ReturnCols = extractColumns(ins.GetReturningList())
		}

	case stmt.GetUpdateStmt() != nil:
		ast.QueryType = QueryUpdate
		upd := stmt.GetUpdateStmt()
		if len(upd.GetReturningList()) > 0 {
			ast.ReturnCols = extractColumns(upd.GetReturningList())
		}
		ast.HasWhere = upd.GetWhereClause() != nil

	case stmt.GetDeleteStmt() != nil:
		ast.QueryType = QueryDelete
		del := stmt.GetDeleteStmt()
		if len(del.GetReturningList()) > 0 {
			ast.ReturnCols = extractColumns(del.GetReturningList())
		}
		ast.HasWhere = del.GetWhereClause() != nil
	}

	return ast, nil
}

func (ast *ASTResult) parseSelect(sel *pgq.SelectStmt) {
	// Extract CTEs.
	if wc := sel.GetWithClause(); wc != nil {
		for _, cteNode := range wc.GetCtes() {
			cte := cteNode.GetCommonTableExpr()
			cteName := cte.GetCtename()
			if innerSel := cte.GetCtequery().GetSelectStmt(); innerSel != nil {
				ast.CTEColumns[cteName] = extractColumns(innerSel.GetTargetList())
			}
		}
	}

	// Extract main SELECT columns.
	ast.SelectCols = extractColumns(sel.GetTargetList())

	// Extract FROM alias map.
	ast.AliasMap = extractAliasMap(sel.GetFromClause())

	// Check for WHERE clause.
	ast.HasWhere = sel.GetWhereClause() != nil
}

// ─── column extraction ───────────────────────────────────────────────────────

// extractColumns classifies each ResTarget in a target list.
func extractColumns(targets []*pgq.Node) []ASTColumn {
	var cols []ASTColumn
	for _, target := range targets {
		rt := target.GetResTarget()
		if rt == nil {
			continue
		}
		cols = append(cols, classifyResTarget(rt))
	}
	return cols
}

// classifyResTarget classifies a single SELECT or RETURNING target.
func classifyResTarget(rt *pgq.ResTarget) ASTColumn {
	alias := rt.GetName()
	val := rt.GetVal()

	switch {
	case val.GetColumnRef() != nil:
		return classifyColumnRef(val.GetColumnRef(), alias)

	case val.GetFuncCall() != nil:
		return classifyFuncCall(val.GetFuncCall(), alias)

	case val.GetSubLink() != nil:
		return classifySubLink(val.GetSubLink(), alias)

	case val.GetCoalesceExpr() != nil:
		return classifyCoalesce(val.GetCoalesceExpr(), alias)

	case val.GetAConst() != nil:
		return classifyConst(val.GetAConst(), alias)

	case val.GetCaseExpr() != nil:
		return classifyCaseExpr(val.GetCaseExpr(), alias)

	default:
		// Check for star.
		if val.GetAStar() != nil {
			return ASTColumn{Name: "*", NodeType: "star"}
		}
		name := alias
		if name == "" {
			name = "unknown"
		}
		return ASTColumn{Name: name, NodeType: "unknown", InferredType: "any"}
	}
}

func classifyColumnRef(cr *pgq.ColumnRef, alias string) ASTColumn {
	parts := columnRefParts(cr)
	colName := parts[len(parts)-1]
	tableAlias := ""
	if len(parts) > 1 {
		tableAlias = parts[0]
	}

	// Star: SELECT * or SELECT t.*
	if colName == "*" {
		return ASTColumn{
			Name:        "*",
			SourceTable: tableAlias,
			NodeType:    "star",
		}
	}

	name := alias
	if name == "" {
		name = colName
	}

	return ASTColumn{
		Name:        name,
		SourceTable: tableAlias,
		SourceCol:   colName,
		NodeType:    "column",
		InferredType: "", // needs schema lookup
	}
}

func classifyFuncCall(fc *pgq.FuncCall, alias string) ASTColumn {
	fname := funcCallNameAST(fc)
	name := alias
	if name == "" {
		name = fname
	}
	goType := inferFuncGoTypeAST(fname, fc)
	return ASTColumn{
		Name:         name,
		NodeType:     "func",
		FuncName:     fname,
		InferredType: goType,
	}
}

func classifySubLink(sl *pgq.SubLink, alias string) ASTColumn {
	name := alias
	if name == "" {
		name = "sublink"
	}

	switch sl.GetSubLinkType() {
	case pgq.SubLinkType_EXISTS_SUBLINK:
		return ASTColumn{Name: name, NodeType: "sublink_exists", InferredType: "bool"}

	case pgq.SubLinkType_EXPR_SUBLINK:
		// Look at what the inner SELECT returns.
		if innerSel := sl.GetSubselect().GetSelectStmt(); innerSel != nil {
			for _, t := range innerSel.GetTargetList() {
				if fc := t.GetResTarget().GetVal().GetFuncCall(); fc != nil {
					fname := funcCallNameAST(fc)
					goType := inferFuncGoTypeAST(fname, fc)
					return ASTColumn{
						Name:         name,
						NodeType:     "sublink_expr",
						FuncName:     fname,
						InferredType: goType,
					}
				}
			}
		}
		return ASTColumn{Name: name, NodeType: "sublink_expr", InferredType: "any"}

	default:
		return ASTColumn{Name: name, NodeType: "sublink", InferredType: "any"}
	}
}

func classifyCoalesce(ce *pgq.CoalesceExpr, alias string) ASTColumn {
	name := alias
	if name == "" {
		name = "coalesce"
	}

	if len(ce.GetArgs()) > 0 {
		firstArg := ce.GetArgs()[0]

		// COALESCE(func(...), ...) → use func's type.
		if fc := firstArg.GetFuncCall(); fc != nil {
			fname := funcCallNameAST(fc)
			goType := inferFuncGoTypeAST(fname, fc)
			return ASTColumn{Name: name, NodeType: "coalesce", FuncName: fname, InferredType: goType}
		}

		// COALESCE(col, ...) → check fallback constant first, then column name.
		if cr := firstArg.GetColumnRef(); cr != nil {
			parts := columnRefParts(cr)
			colName := parts[len(parts)-1]

			// Check if fallback arg is a constant — use its type.
			if len(ce.GetArgs()) >= 2 {
				lastArg := ce.GetArgs()[len(ce.GetArgs())-1]
				if ac := lastArg.GetAConst(); ac != nil {
					goType := constType(ac)
					return ASTColumn{
						Name:         name,
						SourceCol:    colName,
						NodeType:     "coalesce",
						InferredType: goType,
					}
				}
			}

			return ASTColumn{
				Name:         name,
				SourceCol:    colName,
				NodeType:     "coalesce",
				InferredType: "", // needs schema lookup
			}
		}

		// COALESCE(expr, 0) → infer from fallback constant.
		if len(ce.GetArgs()) >= 2 {
			lastArg := ce.GetArgs()[len(ce.GetArgs())-1]
			if ac := lastArg.GetAConst(); ac != nil {
				goType := constType(ac)
				return ASTColumn{Name: name, NodeType: "coalesce", InferredType: goType}
			}
		}
	}

	return ASTColumn{Name: name, NodeType: "coalesce", InferredType: "any"}
}

func classifyConst(ac *pgq.A_Const, alias string) ASTColumn {
	name := alias
	if name == "" {
		name = "const"
	}
	return ASTColumn{Name: name, NodeType: "const", InferredType: constType(ac)}
}

func classifyCaseExpr(_ *pgq.CaseExpr, alias string) ASTColumn {
	name := alias
	if name == "" {
		name = "case"
	}
	// CASE expressions could return any type; caller should check THEN clauses.
	return ASTColumn{Name: name, NodeType: "case", InferredType: "any"}
}

// ─── alias map extraction ────────────────────────────────────────────────────

// extractAliasMap walks the FROM clause and maps table aliases to real table/CTE names.
func extractAliasMap(fromList []*pgq.Node) map[string]string {
	m := make(map[string]string)
	for _, node := range fromList {
		collectFromAliases(node, m)
	}
	return m
}

func collectFromAliases(node *pgq.Node, m map[string]string) {
	switch {
	case node.GetRangeVar() != nil:
		rv := node.GetRangeVar()
		tableName := rv.GetRelname()
		if rv.GetAlias() != nil {
			m[rv.GetAlias().GetAliasname()] = tableName
		} else {
			m[tableName] = tableName
		}

	case node.GetJoinExpr() != nil:
		je := node.GetJoinExpr()
		if je.GetLarg() != nil {
			collectFromAliases(je.GetLarg(), m)
		}
		if je.GetRarg() != nil {
			collectFromAliases(je.GetRarg(), m)
		}
	}
}

// ─── type inference helpers ──────────────────────────────────────────────────

// inferFuncGoTypeAST returns the Go type for a Postgres function from its AST node.
func inferFuncGoTypeAST(fname string, fc *pgq.FuncCall) string {
	lower := strings.ToLower(fname)
	switch lower {
	case "count":
		return "int64"
	case "sum":
		return "int64"
	case "avg":
		return "float64"
	case "max", "min":
		// Type depends on inner column — check for time patterns.
		if len(fc.GetArgs()) > 0 {
			arg := fc.GetArgs()[0]
			if cr := arg.GetColumnRef(); cr != nil {
				parts := columnRefParts(cr)
				colName := parts[len(parts)-1]
				if strings.HasSuffix(colName, "_at") || strings.HasSuffix(colName, "_date") {
					return "time.Time"
				}
			}
		}
		return "" // needs schema lookup
	case "now":
		return "time.Time"
	case "exists":
		return "bool"
	case "coalesce":
		return "" // handled separately by classifyCoalesce
	case "lower", "upper", "trim", "concat", "left", "right", "substring", "replace":
		return "string"
	case "row_number", "rank", "dense_rank", "ntile":
		return "int64"
	case "bool_and", "bool_or", "every":
		return "bool"
	case "array_agg":
		return "" // depends on inner type
	case "jsonb_agg", "json_agg", "jsonb_build_object", "json_build_object":
		return "json.RawMessage"
	case "length", "char_length", "octet_length", "position":
		return "int64"
	case "abs", "ceil", "ceiling", "floor", "round", "trunc":
		return "" // numeric, depends on arg
	default:
		return ""
	}
}

// constType returns the Go type for a constant AST node.
func constType(ac *pgq.A_Const) string {
	switch {
	case ac.GetIval() != nil:
		return "int64"
	case ac.GetSval() != nil:
		return "string"
	case ac.GetBoolval() != nil:
		return "bool"
	default:
		return "any"
	}
}

// ─── AST helpers ─────────────────────────────────────────────────────────────

// funcCallNameAST extracts the function name from a FuncCall node.
func funcCallNameAST(fc *pgq.FuncCall) string {
	var parts []string
	for _, n := range fc.GetFuncname() {
		parts = append(parts, n.GetString_().GetSval())
	}
	return strings.Join(parts, ".")
}

// columnRefParts extracts the dotted parts from a ColumnRef.
func columnRefParts(cr *pgq.ColumnRef) []string {
	var parts []string
	for _, f := range cr.GetFields() {
		if s := f.GetString_(); s != nil {
			parts = append(parts, s.GetSval())
		} else if f.GetAStar() != nil {
			parts = append(parts, "*")
		}
	}
	return parts
}
