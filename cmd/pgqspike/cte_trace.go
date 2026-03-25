package main

import (
	"fmt"
	"strings"

	pgq "github.com/pganalyze/pg_query_go/v6"
	pg_query "github.com/wasilibs/go-pgquery"
)

// CTEColumnInfo holds type info for a CTE-derived column.
type CTEColumnInfo struct {
	Name     string
	NodeType string // "column", "func", "sublink_exists", "sublink_expr", "coalesce", "const", "unknown"
	FuncName string // for func nodes: "count", "max", etc.
	InferredGoType string
}

// buildCTEColumnMap parses CTE definitions and infers types for their output columns.
func buildCTEColumnMap(sql string) (map[string]map[string]CTEColumnInfo, error) {
	result, err := pg_query.Parse(sql)
	if err != nil {
		return nil, err
	}

	cteMap := make(map[string]map[string]CTEColumnInfo)

	for _, stmt := range result.Stmts {
		sel := stmt.Stmt.GetSelectStmt()
		if sel == nil {
			continue
		}
		wc := sel.GetWithClause()
		if wc == nil {
			continue
		}

		for _, cteNode := range wc.GetCtes() {
			cte := cteNode.GetCommonTableExpr()
			cteName := cte.GetCtename()
			innerSel := cte.GetCtequery().GetSelectStmt()
			if innerSel == nil {
				continue
			}

			cols := make(map[string]CTEColumnInfo)
			for _, target := range innerSel.GetTargetList() {
				rt := target.GetResTarget()
				info := inferTargetType(rt)
				cols[info.Name] = info
			}
			cteMap[cteName] = cols
		}
	}

	return cteMap, nil
}

func inferTargetType(rt *pgq.ResTarget) CTEColumnInfo {
	alias := rt.GetName()
	val := rt.GetVal()

	switch {
	case val.GetColumnRef() != nil:
		name := alias
		if name == "" {
			parts := colRefParts(val.GetColumnRef())
			name = parts[len(parts)-1]
		}
		return CTEColumnInfo{Name: name, NodeType: "column", InferredGoType: ""} // needs schema lookup

	case val.GetFuncCall() != nil:
		fc := val.GetFuncCall()
		fname := funcCallName(fc)
		name := alias
		if name == "" {
			name = fname
		}
		goType := inferFuncGoType(fname, fc)
		return CTEColumnInfo{Name: name, NodeType: "func", FuncName: fname, InferredGoType: goType}

	case val.GetSubLink() != nil:
		sl := val.GetSubLink()
		name := alias
		if name == "" {
			name = "sublink"
		}

		switch sl.GetSubLinkType() {
		case pgq.SubLinkType_EXISTS_SUBLINK:
			return CTEColumnInfo{Name: name, NodeType: "sublink_exists", InferredGoType: "bool"}
		case pgq.SubLinkType_EXPR_SUBLINK:
			// Look at what the inner SELECT returns
			if innerSel := sl.GetSubselect().GetSelectStmt(); innerSel != nil {
				for _, t := range innerSel.GetTargetList() {
					if fc := t.GetResTarget().GetVal().GetFuncCall(); fc != nil {
						fname := funcCallName(fc)
						goType := inferFuncGoType(fname, fc)
						return CTEColumnInfo{Name: name, NodeType: "sublink_expr", FuncName: fname, InferredGoType: goType}
					}
				}
			}
			return CTEColumnInfo{Name: name, NodeType: "sublink_expr", InferredGoType: "any"}
		default:
			return CTEColumnInfo{Name: name, NodeType: "sublink", InferredGoType: "any"}
		}

	case val.GetCoalesceExpr() != nil:
		name := alias
		if name == "" {
			name = "coalesce"
		}
		// Infer from first argument
		ce := val.GetCoalesceExpr()
		if len(ce.GetArgs()) > 0 {
			firstArg := ce.GetArgs()[0]
			if fc := firstArg.GetFuncCall(); fc != nil {
				fname := funcCallName(fc)
				goType := inferFuncGoType(fname, fc)
				return CTEColumnInfo{Name: name, NodeType: "coalesce", FuncName: fname, InferredGoType: goType}
			}
			if firstArg.GetColumnRef() != nil {
				return CTEColumnInfo{Name: name, NodeType: "coalesce", InferredGoType: ""} // needs schema lookup
			}
		}
		return CTEColumnInfo{Name: name, NodeType: "coalesce", InferredGoType: "any"}

	case val.GetAConst() != nil:
		name := alias
		if name == "" {
			name = "const"
		}
		ac := val.GetAConst()
		goType := "any"
		if ac.GetIval() != nil {
			goType = "int64"
		} else if ac.GetSval() != nil {
			goType = "string"
		} else if ac.GetBoolval() != nil {
			goType = "bool"
		}
		return CTEColumnInfo{Name: name, NodeType: "const", InferredGoType: goType}

	default:
		name := alias
		if name == "" {
			name = "unknown"
		}
		return CTEColumnInfo{Name: name, NodeType: "unknown", InferredGoType: "any"}
	}
}

func inferFuncGoType(fname string, fc *pgq.FuncCall) string {
	lower := strings.ToLower(fname)
	switch {
	case lower == "count":
		return "int64"
	case lower == "sum":
		return "int64" // could be float64 for non-integer columns
	case lower == "avg":
		return "float64"
	case lower == "max" || lower == "min":
		// Type depends on the inner column — need schema lookup for precision.
		// But we can infer from the column name as a heuristic.
		if len(fc.GetArgs()) > 0 {
			arg := fc.GetArgs()[0]
			if cr := arg.GetColumnRef(); cr != nil {
				parts := colRefParts(cr)
				colName := parts[len(parts)-1]
				if strings.HasSuffix(colName, "_at") || strings.HasSuffix(colName, "_date") {
					return "time.Time"
				}
			}
		}
		return "any" // needs schema lookup
	case lower == "now":
		return "time.Time"
	case lower == "exists":
		return "bool"
	case lower == "coalesce":
		return "any" // handled separately
	case lower == "lower" || lower == "upper" || lower == "trim" || lower == "concat":
		return "string"
	case lower == "row_number" || lower == "rank" || lower == "dense_rank":
		return "int64"
	case lower == "bool_and" || lower == "bool_or" || lower == "every":
		return "bool"
	case lower == "array_agg":
		return "any" // depends on inner type
	case lower == "jsonb_agg" || lower == "json_agg":
		return "json.RawMessage"
	default:
		return "any"
	}
}

func runCTETraceDemo() {
	sql := `WITH recent_events AS (
    SELECT event_type, event_status, COUNT(*) AS event_count, MAX(created_at) AS last_occurred
    FROM security_events
    WHERE user_id = $1 AND created_at > NOW() - INTERVAL '30 days'
    GROUP BY event_type, event_status
),
auth_methods AS (
    SELECT
        EXISTS(SELECT 1 FROM user_passwords WHERE user_id = $1) AS has_password,
        (SELECT COUNT(*) FROM oauth_accounts WHERE user_id = $1 AND account_verified = true) AS oauth_count,
        (SELECT COUNT(*) FROM sessions WHERE user_id = $1 AND expires_at > NOW()) AS active_sessions
)
SELECT u.user_id, u.email, u.email_verified, u.last_login_at,
       am.has_password, am.oauth_count, am.active_sessions,
       (SELECT COUNT(*) FROM recent_events) AS recent_event_count
FROM users u
CROSS JOIN auth_methods am
WHERE u.user_id = $1`

	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("=== CTE COLUMN TRACING DEMO")
	fmt.Println(strings.Repeat("=", 70))

	cteMap, err := buildCTEColumnMap(sql)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	for cteName, cols := range cteMap {
		fmt.Printf("\nCTE '%s':\n", cteName)
		for colName, info := range cols {
			fmt.Printf("  %-20s  node=%-15s  func=%-8s  → %s\n",
				colName, info.NodeType, info.FuncName, info.InferredGoType)
		}
	}

	// Now show how we'd resolve main SELECT columns using CTE info
	fmt.Println("\nResolving main SELECT columns against CTE map + FROM alias map:")

	result, _ := pg_query.Parse(sql)
	sel := result.Stmts[0].Stmt.GetSelectStmt()

	// Build alias → table/CTE name map from FROM clause
	aliasMap := buildAliasMap(sel.GetFromClause())
	fmt.Println("\nFROM alias map:")
	for alias, tableName := range aliasMap {
		fmt.Printf("  %s → %s\n", alias, tableName)
	}
	fmt.Println()

	for _, target := range sel.GetTargetList() {
		rt := target.GetResTarget()
		alias := rt.GetName()
		val := rt.GetVal()

		switch {
		case val.GetColumnRef() != nil:
			cr := val.GetColumnRef()
			parts := colRefParts(cr)
			colName := parts[len(parts)-1]
			tableAlias := ""
			if len(parts) > 1 {
				tableAlias = parts[0]
			}

			displayName := alias
			if displayName == "" {
				displayName = colName
			}

			// Resolve table alias → real table/CTE name
			realName := tableAlias
			if mapped, ok := aliasMap[tableAlias]; ok {
				realName = mapped
			}

			// Try CTE lookup
			resolved := false
			if realName != "" {
				if cteCols, ok := cteMap[realName]; ok {
					if cteCol, ok := cteCols[colName]; ok {
						fmt.Printf("  %-25s → CTE '%s'.%s → %s\n", displayName, realName, colName, cteCol.InferredGoType)
						resolved = true
					}
				}
			}
			if !resolved {
				fmt.Printf("  %-25s → schema lookup (%s.%s)\n", displayName, realName, colName)
			}

		case val.GetSubLink() != nil:
			sl := val.GetSubLink()
			name := alias
			if sl.GetSubLinkType() == pgq.SubLinkType_EXPR_SUBLINK {
				if innerSel := sl.GetSubselect().GetSelectStmt(); innerSel != nil {
					for _, t := range innerSel.GetTargetList() {
						if fc := t.GetResTarget().GetVal().GetFuncCall(); fc != nil {
							goType := inferFuncGoType(funcCallName(fc), fc)
							fmt.Printf("  %-25s → subselect %s(...) → %s\n", name, funcCallName(fc), goType)
						}
					}
				}
			}
		}
	}
}

// buildAliasMap walks the FROM clause and maps table aliases to real table/CTE names.
func buildAliasMap(fromList []*pgq.Node) map[string]string {
	m := make(map[string]string)
	for _, node := range fromList {
		collectAliases(node, m)
	}
	return m
}

func collectAliases(node *pgq.Node, m map[string]string) {
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
			collectAliases(je.GetLarg(), m)
		}
		if je.GetRarg() != nil {
			collectAliases(je.GetRarg(), m)
		}
	}
}
