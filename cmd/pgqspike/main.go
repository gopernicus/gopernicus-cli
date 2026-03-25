package main

import (
	"encoding/json"
	"fmt"
	"strings"

	pgq "github.com/pganalyze/pg_query_go/v6"
	pg_query "github.com/wasilibs/go-pgquery"
)

func main() {
	// Our actual queries from queries.sql, with dynamic placeholders replaced.
	// The question: what do we replace $filters, $order, $lim with so pg_query can parse?

	type testCase struct {
		name    string
		cleaned string // what we'd feed to pg_query after stripping our directives
	}

	queries := []testCase{
		// Simple CRUD - no dynamic stuff
		{
			name:    "GetUser",
			cleaned: `SELECT * FROM users WHERE user_id = $1`,
		},

		// List with $filters, $order, $lim stripped entirely
		{
			name:    "ListUsers",
			cleaned: `SELECT * FROM users`,
		},

		// CTE + filters stripped
		{
			name: "ListUsersWithActivity",
			cleaned: `WITH activity AS (
    SELECT user_id,
           MAX(last_used_at) AS last_active_at,
           COUNT(*) AS session_count
    FROM sessions
    WHERE expires_at > NOW()
    GROUP BY user_id
)
SELECT u.user_id, u.email, u.display_name, u.email_verified,
       u.record_state, u.created_at,
       COALESCE(a.last_active_at, u.last_login_at) AS last_active_at,
       COALESCE(a.session_count, 0) AS session_count
FROM users u
LEFT JOIN activity a ON u.user_id = a.user_id`,
		},

		// Subselect + filters stripped but base WHERE preserved
		{
			name: "ListUsersByTenant",
			cleaned: `SELECT u.user_id, u.email, u.display_name, u.email_verified, u.created_at
FROM users u
WHERE u.user_id IN (
    SELECT rr.subject_id FROM rebac_relationships rr
    WHERE rr.resource_type = 'tenant' AND rr.resource_id = $1 AND rr.subject_type = 'user'
)
AND u.record_state = 'active'`,
		},

		// CTE with scalar subselects and CROSS JOIN
		{
			name: "GetUserSecuritySummary",
			cleaned: `WITH recent_events AS (
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
WHERE u.user_id = $1`,
		},

		// JOIN with aliased column
		{
			name: "GetUserWithPrincipal",
			cleaned: `SELECT u.user_id, u.email, u.display_name, u.email_verified,
       u.last_login_at, u.record_state, u.created_at, u.updated_at,
       p.principal_type, p.created_at AS principal_created_at
FROM users u
INNER JOIN principals p ON u.user_id = p.principal_id
WHERE u.user_id = $1`,
		},

		// Aggregate with COUNT(DISTINCT ...)
		{
			name: "ListResourceCreators",
			cleaned: `SELECT u.user_id, u.email, u.display_name,
       COUNT(DISTINCT t.tenant_id) AS tenants_created,
       COUNT(DISTINCT sa.service_account_id) AS service_accounts_created,
       COUNT(DISTINCT g.group_id) AS groups_created
FROM users u
INNER JOIN principals p ON u.user_id = p.principal_id
LEFT JOIN tenants t ON p.principal_id = t.creator_principal_id AND t.record_state = 'active'
LEFT JOIN service_accounts sa ON p.principal_id = sa.creator_principal_id AND sa.record_state = 'active'
LEFT JOIN groups g ON p.principal_id = g.creator_principal_id AND g.record_state = 'active'
WHERE u.record_state = 'active'
GROUP BY u.user_id, u.email, u.display_name`,
		},

		// UPDATE with RETURNING subset
		{
			name: "VerifyUserEmail",
			cleaned: `UPDATE users SET email_verified = true, updated_at = NOW()
WHERE user_id = $1
RETURNING user_id, email, email_verified, updated_at`,
		},

		// UPDATE with subselect in WHERE + RETURNING
		{
			name: "DeactivateInactiveUsers",
			cleaned: `UPDATE users SET record_state = 'inactive', updated_at = NOW()
WHERE record_state = 'active' AND last_login_at < $1
AND user_id NOT IN (SELECT DISTINCT s.user_id FROM sessions s WHERE s.expires_at > NOW())
RETURNING user_id, email`,
		},
	}

	// Run the CTE tracing demo first
	runCTETraceDemo()

	for _, q := range queries {
		fmt.Printf("\n%s\n", strings.Repeat("=", 70))
		fmt.Printf("=== %s\n", q.name)
		fmt.Printf("%s\n\n", strings.Repeat("=", 70))

		result, err := pg_query.Parse(q.cleaned)
		if err != nil {
			fmt.Printf("PARSE ERROR: %v\n", err)
			fmt.Printf("SQL: %s\n", q.cleaned)
			continue
		}

		for _, stmt := range result.Stmts {
			node := stmt.Stmt

			switch {
			case node.GetSelectStmt() != nil:
				sel := node.GetSelectStmt()
				printSelectInfo(sel, 0)

			case node.GetUpdateStmt() != nil:
				upd := node.GetUpdateStmt()
				fmt.Printf("UPDATE on: %s\n", upd.GetRelation().GetRelname())
				if len(upd.GetReturningList()) > 0 {
					fmt.Println("RETURNING columns:")
					for _, target := range upd.GetReturningList() {
						rt := target.GetResTarget()
						fmt.Print("  ")
						printResTarget(rt)
					}
				}

			case node.GetInsertStmt() != nil:
				ins := node.GetInsertStmt()
				fmt.Printf("INSERT into: %s\n", ins.GetRelation().GetRelname())
				if len(ins.GetReturningList()) > 0 {
					fmt.Println("RETURNING columns:")
					for _, target := range ins.GetReturningList() {
						rt := target.GetResTarget()
						fmt.Print("  ")
						printResTarget(rt)
					}
				}
			}
		}
	}
}

func printSelectInfo(sel *pgq.SelectStmt, depth int) {
	prefix := strings.Repeat("  ", depth)

	// CTEs
	if wc := sel.GetWithClause(); wc != nil {
		fmt.Printf("%sCTEs:\n", prefix)
		for _, cte := range wc.GetCtes() {
			cteDef := cte.GetCommonTableExpr()
			fmt.Printf("%s  CTE: %s\n", prefix, cteDef.GetCtename())
			if inner := cteDef.GetCtequery().GetSelectStmt(); inner != nil {
				fmt.Printf("%s    columns:\n", prefix)
				for _, t := range inner.GetTargetList() {
					rt := t.GetResTarget()
					fmt.Printf("%s      ", prefix)
					printResTarget(rt)
				}
			}
		}
	}

	// Target columns
	fmt.Printf("%sSELECT columns:\n", prefix)
	for _, target := range sel.GetTargetList() {
		rt := target.GetResTarget()
		fmt.Printf("%s  ", prefix)
		printResTarget(rt)
	}

	// FROM clause
	fmt.Printf("%sFROM:\n", prefix)
	for _, from := range sel.GetFromClause() {
		printFromNode(from, prefix+"  ")
	}

	// WHERE
	if wh := sel.GetWhereClause(); wh != nil {
		fmt.Printf("%sHAS WHERE: yes\n", prefix)
	}
}

func printResTarget(rt *pgq.ResTarget) {
	alias := rt.GetName()
	val := rt.GetVal()

	switch {
	case val.GetColumnRef() != nil:
		cr := val.GetColumnRef()
		parts := colRefParts(cr)
		colName := strings.Join(parts, ".")
		if alias != "" {
			fmt.Printf("COLUMN: %s AS %s\n", colName, alias)
		} else {
			fmt.Printf("COLUMN: %s\n", colName)
		}

	case val.GetFuncCall() != nil:
		fc := val.GetFuncCall()
		funcName := funcCallName(fc)
		distinct := ""
		if fc.GetAggDistinct() {
			distinct = "DISTINCT "
		}
		args := []string{}
		for _, arg := range fc.GetArgs() {
			args = append(args, describeNode(arg))
		}
		if fc.GetAggStar() {
			args = []string{"*"}
		}
		if alias != "" {
			fmt.Printf("FUNC: %s(%s%s) AS %s\n", funcName, distinct, strings.Join(args, ", "), alias)
		} else {
			fmt.Printf("FUNC: %s(%s%s)\n", funcName, distinct, strings.Join(args, ", "))
		}

	case val.GetSubLink() != nil:
		sl := val.GetSubLink()
		subType := sl.GetSubLinkType().String()
		// Try to describe what's inside
		inner := ""
		if innerSel := sl.GetSubselect().GetSelectStmt(); innerSel != nil {
			for _, t := range innerSel.GetTargetList() {
				if fc := t.GetResTarget().GetVal().GetFuncCall(); fc != nil {
					inner = funcCallName(fc) + "(...)"
					break
				}
			}
		}
		if alias != "" {
			fmt.Printf("SUBLINK(%s, %s) AS %s\n", subType, inner, alias)
		} else {
			fmt.Printf("SUBLINK(%s, %s)\n", subType, inner)
		}

	case val.GetBoolExpr() != nil:
		if alias != "" {
			fmt.Printf("BOOL_EXPR AS %s\n", alias)
		} else {
			fmt.Printf("BOOL_EXPR\n")
		}

	case val.GetAConst() != nil:
		if alias != "" {
			fmt.Printf("CONST AS %s\n", alias)
		} else {
			fmt.Printf("CONST\n")
		}

	case val.GetCoalesceExpr() != nil:
		ce := val.GetCoalesceExpr()
		args := []string{}
		for _, arg := range ce.GetArgs() {
			args = append(args, describeNode(arg))
		}
		if alias != "" {
			fmt.Printf("COALESCE(%s) AS %s\n", strings.Join(args, ", "), alias)
		} else {
			fmt.Printf("COALESCE(%s)\n", strings.Join(args, ", "))
		}

	default:
		if val.GetAStar() != nil {
			fmt.Printf("* (all columns)\n")
		} else {
			j, _ := json.Marshal(val)
			fmt.Printf("UNKNOWN: %s\n", string(j))
		}
	}
}

func printFromNode(node *pgq.Node, prefix string) {
	switch {
	case node.GetRangeVar() != nil:
		rv := node.GetRangeVar()
		alias := ""
		if rv.GetAlias() != nil {
			alias = " AS " + rv.GetAlias().GetAliasname()
		}
		schema := ""
		if rv.GetSchemaname() != "" {
			schema = rv.GetSchemaname() + "."
		}
		fmt.Printf("%sTABLE: %s%s%s\n", prefix, schema, rv.GetRelname(), alias)

	case node.GetJoinExpr() != nil:
		je := node.GetJoinExpr()
		joinType := je.GetJointype().String()
		fmt.Printf("%sJOIN(%s):\n", prefix, joinType)
		if je.GetLarg() != nil {
			printFromNode(je.GetLarg(), prefix+"  L: ")
		}
		if je.GetRarg() != nil {
			printFromNode(je.GetRarg(), prefix+"  R: ")
		}

	default:
		j, _ := json.Marshal(node)
		fmt.Printf("%sUNKNOWN FROM: %s\n", prefix, string(j))
	}
}

func funcCallName(fc *pgq.FuncCall) string {
	parts := []string{}
	for _, n := range fc.GetFuncname() {
		parts = append(parts, n.GetString_().GetSval())
	}
	return strings.Join(parts, ".")
}

func colRefParts(cr *pgq.ColumnRef) []string {
	parts := []string{}
	for _, f := range cr.GetFields() {
		if s := f.GetString_(); s != nil {
			parts = append(parts, s.GetSval())
		} else if f.GetAStar() != nil {
			parts = append(parts, "*")
		}
	}
	return parts
}

func describeNode(node *pgq.Node) string {
	switch {
	case node.GetColumnRef() != nil:
		return strings.Join(colRefParts(node.GetColumnRef()), ".")
	case node.GetAConst() != nil:
		ac := node.GetAConst()
		if ac.GetIval() != nil {
			return fmt.Sprintf("%d", ac.GetIval().GetIval())
		}
		if ac.GetSval() != nil {
			return fmt.Sprintf("'%s'", ac.GetSval().GetSval())
		}
		if ac.GetBoolval() != nil {
			return fmt.Sprintf("%v", ac.GetBoolval().GetBoolval())
		}
		return "const"
	case node.GetFuncCall() != nil:
		fc := node.GetFuncCall()
		name := funcCallName(fc)
		if fc.GetAggStar() {
			return name + "(*)"
		}
		args := []string{}
		for _, a := range fc.GetArgs() {
			args = append(args, describeNode(a))
		}
		return name + "(" + strings.Join(args, ", ") + ")"
	case node.GetSubLink() != nil:
		return "(SUBSELECT)"
	case node.GetCoalesceExpr() != nil:
		ce := node.GetCoalesceExpr()
		args := []string{}
		for _, a := range ce.GetArgs() {
			args = append(args, describeNode(a))
		}
		return "COALESCE(" + strings.Join(args, ", ") + ")"
	case node.GetParamRef() != nil:
		return fmt.Sprintf("$%d", node.GetParamRef().GetNumber())
	case node.GetTypeCast() != nil:
		return describeNode(node.GetTypeCast().GetArg()) + "::cast"
	default:
		return "?"
	}
}
