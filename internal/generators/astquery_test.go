package generators

import (
	"strings"
	"testing"
)

func TestPrepareForParse_SimpleSelect(t *testing.T) {
	sql := `SELECT * FROM users WHERE user_id = @user_id`
	got := PrepareForParse(sql, nil)
	if !strings.Contains(got, "$1") {
		t.Errorf("expected $1, got: %s", got)
	}
	if strings.Contains(got, "@user_id") {
		t.Errorf("expected @user_id to be replaced: %s", got)
	}
}

func TestPrepareForParse_NamedFiltersAndSearch(t *testing.T) {
	sql := `SELECT * FROM users WHERE $conditions AND $search ORDER BY $order LIMIT $limit`
	got := PrepareForParse(sql, []string{"conditions"})
	if strings.Contains(got, "$conditions") {
		t.Errorf("expected $conditions to be replaced: %s", got)
	}
	if strings.Contains(got, "$search") {
		t.Errorf("expected $search to be replaced: %s", got)
	}
	if strings.Contains(got, "$order") {
		t.Errorf("expected ORDER BY line to be stripped: %s", got)
	}
	if strings.Contains(got, "$limit") {
		t.Errorf("expected LIMIT line to be stripped: %s", got)
	}
	if !strings.Contains(got, "TRUE") {
		t.Errorf("expected TRUE placeholder: %s", got)
	}
}

func TestPrepareForParse_MultipleFilters(t *testing.T) {
	sql := `SELECT * FROM users WHERE $conditions AND $status AND $search`
	got := PrepareForParse(sql, []string{"conditions", "status"})
	if strings.Contains(got, "$conditions") || strings.Contains(got, "$status") || strings.Contains(got, "$search") {
		t.Errorf("expected all placeholders replaced: %s", got)
	}
}

func TestPrepareForParse_InsertWithFieldsValues(t *testing.T) {
	sql := `INSERT INTO users ($fields) VALUES ($values) RETURNING *`
	got := PrepareForParse(sql, nil)
	if strings.Contains(got, "$fields") || strings.Contains(got, "$values") {
		t.Errorf("expected $fields/$values replaced: %s", got)
	}
}

func TestPrepareForParse_UpdateWithFields(t *testing.T) {
	sql := `UPDATE users SET $fields WHERE user_id = @user_id RETURNING *`
	got := PrepareForParse(sql, nil)
	if strings.Contains(got, "$fields") {
		t.Errorf("expected $fields replaced: %s", got)
	}
	if !strings.Contains(got, "$1") {
		t.Errorf("expected @user_id to become $1: %s", got)
	}
}

func TestPrepareForParse_ParamNumbering(t *testing.T) {
	sql := `SELECT * FROM users WHERE user_id = @user_id AND email = @email AND user_id = @user_id`
	got := PrepareForParse(sql, nil)
	// user_id should be $1, email should be $2, second user_id should reuse $1.
	if strings.Count(got, "$1") != 2 {
		t.Errorf("expected $1 to appear twice: %s", got)
	}
	if !strings.Contains(got, "$2") {
		t.Errorf("expected $2 for email: %s", got)
	}
}

func TestParseSQL_SimpleSelect(t *testing.T) {
	sql := `SELECT * FROM users WHERE user_id = $1`
	ast, err := ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL: %v", err)
	}
	if ast.QueryType != QuerySelect {
		t.Errorf("QueryType = %v, want QuerySelect", ast.QueryType)
	}
	if len(ast.SelectCols) != 1 || ast.SelectCols[0].NodeType != "star" {
		t.Errorf("expected star column, got: %+v", ast.SelectCols)
	}
	if !ast.HasWhere {
		t.Error("expected HasWhere=true")
	}
}

func TestParseSQL_ExplicitColumns(t *testing.T) {
	sql := `SELECT user_id, email, display_name FROM users WHERE user_id = $1`
	ast, err := ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL: %v", err)
	}
	if len(ast.SelectCols) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(ast.SelectCols))
	}
	names := []string{ast.SelectCols[0].Name, ast.SelectCols[1].Name, ast.SelectCols[2].Name}
	if names[0] != "user_id" || names[1] != "email" || names[2] != "display_name" {
		t.Errorf("column names = %v, want [user_id email display_name]", names)
	}
	for _, col := range ast.SelectCols {
		if col.NodeType != "column" {
			t.Errorf("column %q: NodeType = %q, want column", col.Name, col.NodeType)
		}
	}
}

func TestParseSQL_AliasedColumns(t *testing.T) {
	sql := `SELECT u.user_id, u.email, p.created_at AS principal_created_at
FROM users u INNER JOIN principals p ON u.user_id = p.principal_id WHERE u.user_id = $1`
	ast, err := ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL: %v", err)
	}
	if len(ast.SelectCols) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(ast.SelectCols))
	}

	// u.user_id → SourceTable="u", Name="user_id"
	if ast.SelectCols[0].SourceTable != "u" || ast.SelectCols[0].Name != "user_id" {
		t.Errorf("col[0] = %+v", ast.SelectCols[0])
	}

	// p.created_at AS principal_created_at → Name="principal_created_at", SourceCol="created_at"
	col2 := ast.SelectCols[2]
	if col2.Name != "principal_created_at" || col2.SourceCol != "created_at" || col2.SourceTable != "p" {
		t.Errorf("col[2] = %+v", col2)
	}

	// Alias map.
	if ast.AliasMap["u"] != "users" {
		t.Errorf("AliasMap[u] = %q, want users", ast.AliasMap["u"])
	}
	if ast.AliasMap["p"] != "principals" {
		t.Errorf("AliasMap[p] = %q, want principals", ast.AliasMap["p"])
	}
}

func TestParseSQL_AggregateColumns(t *testing.T) {
	sql := `SELECT u.user_id, COUNT(DISTINCT t.tenant_id) AS tenants_created
FROM users u LEFT JOIN tenants t ON u.user_id = t.creator_id
GROUP BY u.user_id`
	ast, err := ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL: %v", err)
	}
	if len(ast.SelectCols) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(ast.SelectCols))
	}

	agg := ast.SelectCols[1]
	if agg.Name != "tenants_created" {
		t.Errorf("Name = %q, want tenants_created", agg.Name)
	}
	if agg.NodeType != "func" || agg.FuncName != "count" {
		t.Errorf("NodeType=%q FuncName=%q, want func/count", agg.NodeType, agg.FuncName)
	}
	if agg.InferredType != "int64" {
		t.Errorf("InferredType = %q, want int64", agg.InferredType)
	}
}

func TestParseSQL_CTEWithTypeInference(t *testing.T) {
	sql := `WITH activity AS (
    SELECT user_id, MAX(last_used_at) AS last_active_at, COUNT(*) AS session_count
    FROM sessions
    WHERE expires_at > NOW()
    GROUP BY user_id
)
SELECT u.user_id, u.email,
       COALESCE(a.last_active_at, u.last_login_at) AS last_active_at,
       COALESCE(a.session_count, 0) AS session_count
FROM users u LEFT JOIN activity a ON u.user_id = a.user_id`

	ast, err := ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL: %v", err)
	}

	// CTE columns.
	activityCols, ok := ast.CTEColumns["activity"]
	if !ok {
		t.Fatal("expected CTE 'activity'")
	}
	if len(activityCols) != 3 {
		t.Fatalf("expected 3 CTE columns, got %d", len(activityCols))
	}

	// Find session_count in CTE — should be int64 from COUNT(*).
	var sessionCount *ASTColumn
	for i := range activityCols {
		if activityCols[i].Name == "session_count" {
			sessionCount = &activityCols[i]
			break
		}
	}
	if sessionCount == nil {
		t.Fatal("expected session_count in CTE columns")
	}
	if sessionCount.InferredType != "int64" {
		t.Errorf("CTE session_count type = %q, want int64", sessionCount.InferredType)
	}

	// Find last_active_at in CTE — MAX(last_used_at) → time.Time.
	var lastActive *ASTColumn
	for i := range activityCols {
		if activityCols[i].Name == "last_active_at" {
			lastActive = &activityCols[i]
			break
		}
	}
	if lastActive == nil {
		t.Fatal("expected last_active_at in CTE columns")
	}
	if lastActive.InferredType != "time.Time" {
		t.Errorf("CTE last_active_at type = %q, want time.Time", lastActive.InferredType)
	}

	// Main SELECT: COALESCE(a.session_count, 0) AS session_count → int64 from fallback.
	if len(ast.SelectCols) != 4 {
		t.Fatalf("expected 4 SELECT columns, got %d", len(ast.SelectCols))
	}
	var mainSessionCount *ASTColumn
	for i := range ast.SelectCols {
		if ast.SelectCols[i].Name == "session_count" {
			mainSessionCount = &ast.SelectCols[i]
			break
		}
	}
	if mainSessionCount == nil {
		t.Fatal("expected session_count in SELECT columns")
	}
	if mainSessionCount.NodeType != "coalesce" {
		t.Errorf("session_count NodeType = %q, want coalesce", mainSessionCount.NodeType)
	}
	if mainSessionCount.InferredType != "int64" {
		t.Errorf("session_count InferredType = %q, want int64", mainSessionCount.InferredType)
	}
}

func TestParseSQL_SecuritySummaryCTE(t *testing.T) {
	sql := `WITH auth_methods AS (
    SELECT
        EXISTS(SELECT 1 FROM user_passwords WHERE user_id = $1) AS has_password,
        (SELECT COUNT(*) FROM oauth_accounts WHERE user_id = $1 AND account_verified = true) AS oauth_count,
        (SELECT COUNT(*) FROM sessions WHERE user_id = $1 AND expires_at > NOW()) AS active_sessions
)
SELECT u.user_id, u.email,
       am.has_password, am.oauth_count, am.active_sessions
FROM users u
CROSS JOIN auth_methods am
WHERE u.user_id = $1`

	ast, err := ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL: %v", err)
	}

	// CTE columns.
	authCols, ok := ast.CTEColumns["auth_methods"]
	if !ok {
		t.Fatal("expected CTE 'auth_methods'")
	}

	colTypes := make(map[string]string)
	for _, col := range authCols {
		colTypes[col.Name] = col.InferredType
	}

	// EXISTS → bool.
	if colTypes["has_password"] != "bool" {
		t.Errorf("has_password type = %q, want bool", colTypes["has_password"])
	}
	// (SELECT COUNT(*)) → int64 (sublink_expr with count).
	if colTypes["oauth_count"] != "int64" {
		t.Errorf("oauth_count type = %q, want int64", colTypes["oauth_count"])
	}
	if colTypes["active_sessions"] != "int64" {
		t.Errorf("active_sessions type = %q, want int64", colTypes["active_sessions"])
	}

	// Alias map: u → users, am → auth_methods.
	if ast.AliasMap["am"] != "auth_methods" {
		t.Errorf("AliasMap[am] = %q, want auth_methods", ast.AliasMap["am"])
	}
}

func TestParseSQL_UpdateReturning(t *testing.T) {
	sql := `UPDATE users SET email_verified = true, updated_at = NOW()
WHERE user_id = $1
RETURNING user_id, email, email_verified, updated_at`

	ast, err := ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL: %v", err)
	}

	if ast.QueryType != QueryUpdate {
		t.Errorf("QueryType = %v, want QueryUpdate", ast.QueryType)
	}
	if len(ast.ReturnCols) != 4 {
		t.Fatalf("expected 4 RETURNING columns, got %d", len(ast.ReturnCols))
	}
	names := make([]string, len(ast.ReturnCols))
	for i, c := range ast.ReturnCols {
		names[i] = c.Name
	}
	expected := []string{"user_id", "email", "email_verified", "updated_at"}
	for i, want := range expected {
		if names[i] != want {
			t.Errorf("ReturnCols[%d] = %q, want %q", i, names[i], want)
		}
	}
}

func TestParseSQL_SubselectInMainQuery(t *testing.T) {
	sql := `SELECT u.user_id, u.email,
       (SELECT COUNT(*) FROM recent_events) AS recent_event_count
FROM users u WHERE u.user_id = $1`

	ast, err := ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL: %v", err)
	}

	if len(ast.SelectCols) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(ast.SelectCols))
	}

	subCol := ast.SelectCols[2]
	if subCol.Name != "recent_event_count" {
		t.Errorf("Name = %q, want recent_event_count", subCol.Name)
	}
	if subCol.NodeType != "sublink_expr" {
		t.Errorf("NodeType = %q, want sublink_expr", subCol.NodeType)
	}
	if subCol.InferredType != "int64" {
		t.Errorf("InferredType = %q, want int64", subCol.InferredType)
	}
}

func TestParseSQL_InsertReturning(t *testing.T) {
	sql := `INSERT INTO users (col1) VALUES ($1) RETURNING *`

	ast, err := ParseSQL(sql)
	if err != nil {
		t.Fatalf("ParseSQL: %v", err)
	}

	if ast.QueryType != QueryInsert {
		t.Errorf("QueryType = %v, want QueryInsert", ast.QueryType)
	}
	if len(ast.ReturnCols) != 1 || ast.ReturnCols[0].NodeType != "star" {
		t.Errorf("expected star RETURNING, got: %+v", ast.ReturnCols)
	}
}

func TestPrepareAndParse_EndToEnd(t *testing.T) {
	// Simulates the full pipeline: annotated SQL → prepare → parse.
	sql := `SELECT u.user_id, u.email, u.display_name
FROM users u
WHERE u.user_id IN (
    SELECT rr.subject_id FROM rebac_relationships rr
    WHERE rr.resource_type = 'tenant' AND rr.resource_id = @tenant_id AND rr.subject_type = 'user'
)
AND $conditions AND $search
ORDER BY $order
LIMIT $limit`

	prepared := PrepareForParse(sql, []string{"conditions"})
	ast, err := ParseSQL(prepared)
	if err != nil {
		t.Fatalf("ParseSQL after prepare: %v\nPrepared SQL:\n%s", err, prepared)
	}

	if ast.QueryType != QuerySelect {
		t.Errorf("QueryType = %v, want QuerySelect", ast.QueryType)
	}
	if len(ast.SelectCols) != 3 {
		t.Errorf("expected 3 columns, got %d: %+v", len(ast.SelectCols), ast.SelectCols)
	}
	if ast.AliasMap["u"] != "users" {
		t.Errorf("AliasMap[u] = %q, want users", ast.AliasMap["u"])
	}
}
