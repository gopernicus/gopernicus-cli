package generators

import (
	"strings"
	"testing"
)

const sampleNewFormat = `-- @database: primary

-- @func: ListUsers
-- @filter:conditions *,-record_state
-- @search: ilike(email, display_name)
-- @order: *,-record_state
-- @max: 100
SELECT *
FROM users
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: GetUser
SELECT *
FROM users
WHERE user_id = @user_id
;

-- @func: CreateUser
-- @fields: *,-created_at,-updated_at
INSERT INTO users
($fields)
VALUES ($values)
RETURNING *;

-- @func: UpdateUser
-- @fields: *,-user_id,-created_at
UPDATE users
SET $fields
WHERE user_id = @user_id
RETURNING *;

-- @func: SoftDeleteUser
UPDATE users
SET record_state = 'deleted'
WHERE user_id = @user_id
;

-- @func: DeleteUser
DELETE FROM users
WHERE user_id = @user_id
;
`

func TestParseString_NewFormat(t *testing.T) {
	f, err := ParseString(sampleNewFormat)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}

	if f.Database != "primary" {
		t.Errorf("Database = %q, want %q", f.Database, "primary")
	}
	if f.Table != "" {
		t.Errorf("Table = %q, want empty", f.Table)
	}

	if len(f.Queries) != 6 {
		t.Fatalf("got %d queries, want 6", len(f.Queries))
	}

	// ─── ListUsers ──────────────────────────────────────────────────────
	q := f.Queries[0]
	if q.Name != "ListUsers" {
		t.Errorf("Name = %q, want %q", q.Name, "ListUsers")
	}
	if q.Type != QuerySelect {
		t.Errorf("Type = %v, want QuerySelect", q.Type)
	}
	if !q.HasFilters {
		t.Error("ListUsers: expected HasFilters=true")
	}
	if !q.HasOrder {
		t.Error("ListUsers: expected HasOrder=true")
	}
	if !q.HasLimit {
		t.Error("ListUsers: expected HasLimit=true")
	}
	if q.Filters["conditions"] != "*,-record_state" {
		t.Errorf("ListUsers Filters[conditions] = %q, want %q", q.Filters["conditions"], "*,-record_state")
	}
	if q.OrderSpec != "*,-record_state" {
		t.Errorf("ListUsers OrderSpec = %q, want %q", q.OrderSpec, "*,-record_state")
	}
	if q.LimitSpec != "100" {
		t.Errorf("ListUsers LimitSpec = %q, want %q", q.LimitSpec, "100")
	}
	if q.SearchSpec != "ilike(email, display_name)" {
		t.Errorf("ListUsers SearchSpec = %q, want %q", q.SearchSpec, "ilike(email, display_name)")
	}
	if !q.HasSearch {
		t.Error("ListUsers: expected HasSearch=true")
	}
	if !q.ReturnsRows {
		t.Error("ListUsers: expected ReturnsRows=true")
	}

	// ─── GetUser ────────────────────────────────────────────────────────
	q = f.Queries[1]
	if q.Name != "GetUser" {
		t.Errorf("Name = %q, want %q", q.Name, "GetUser")
	}
	if q.Type != QuerySelect {
		t.Errorf("Type = %v, want QuerySelect", q.Type)
	}
	assertParams(t, q, "user_id")
	if !q.ReturnsRows {
		t.Error("GetUser: expected ReturnsRows=true")
	}

	// ─── CreateUser ─────────────────────────────────────────────────────
	q = f.Queries[2]
	if q.Name != "CreateUser" {
		t.Errorf("Name = %q, want %q", q.Name, "CreateUser")
	}
	if q.Type != QueryInsert {
		t.Errorf("Type = %v, want QueryInsert", q.Type)
	}
	if !q.HasFields {
		t.Error("CreateUser: expected HasFields=true")
	}
	if !q.HasValues {
		t.Error("CreateUser: expected HasValues=true")
	}
	if !q.ReturnsRows {
		t.Error("CreateUser: expected ReturnsRows=true")
	}
	if q.Annotations["fields"] != "*,-created_at,-updated_at" {
		t.Errorf("CreateUser @fields = %q", q.Annotations["fields"])
	}

	// ─── UpdateUser ─────────────────────────────────────────────────────
	q = f.Queries[3]
	if q.Name != "UpdateUser" {
		t.Errorf("Name = %q, want %q", q.Name, "UpdateUser")
	}
	if q.Type != QueryUpdate {
		t.Errorf("Type = %v, want QueryUpdate", q.Type)
	}
	if !q.HasFields {
		t.Error("UpdateUser: expected HasFields=true")
	}
	assertParams(t, q, "user_id")
	if !q.ReturnsRows {
		t.Error("UpdateUser: expected ReturnsRows=true")
	}

	// ─── SoftDeleteUser ─────────────────────────────────────────────────
	q = f.Queries[4]
	if q.Name != "SoftDeleteUser" {
		t.Errorf("Name = %q, want %q", q.Name, "SoftDeleteUser")
	}
	if q.Type != QueryUpdate {
		t.Errorf("Type = %v, want QueryUpdate", q.Type)
	}
	assertParams(t, q, "user_id")
	if q.ReturnsRows {
		t.Error("SoftDeleteUser: expected ReturnsRows=false")
	}

	// ─── DeleteUser ─────────────────────────────────────────────────────
	q = f.Queries[5]
	if q.Name != "DeleteUser" {
		t.Errorf("Name = %q, want %q", q.Name, "DeleteUser")
	}
	if q.Type != QueryDelete {
		t.Errorf("Type = %v, want QueryDelete", q.Type)
	}
	assertParams(t, q, "user_id")
}

func TestParseString_NoSoftDelete(t *testing.T) {
	input := `-- @database: primary

-- @func: ListWidgets
-- @filter:conditions *
-- @order: *
-- @max: 100
SELECT *
FROM widgets
WHERE $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: DeleteWidget
DELETE FROM widgets
WHERE widget_id = @widget_id
;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}

	if len(f.Queries) != 2 {
		t.Fatalf("got %d queries, want 2", len(f.Queries))
	}

	q := f.Queries[0]
	if q.Filters["conditions"] != "*" {
		t.Errorf("Filters[conditions] = %q, want %q", q.Filters["conditions"], "*")
	}
	if q.OrderSpec != "*" {
		t.Errorf("OrderSpec = %q, want %q", q.OrderSpec, "*")
	}
	if q.LimitSpec != "100" {
		t.Errorf("LimitSpec = %q, want %q", q.LimitSpec, "100")
	}

	q = f.Queries[1]
	if q.Type != QueryDelete {
		t.Errorf("Type = %v, want QueryDelete", q.Type)
	}
}

func TestParseString_MultiLineSQL(t *testing.T) {
	input := `-- @func: CustomJoin
-- @filter:conditions email,display_name
-- @search: ilike(email, display_name)
SELECT u.user_id, u.email, p.profile_id
    FROM users u
    LEFT JOIN profiles p ON u.user_id = p.profile_id
    WHERE $conditions
;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}

	if len(f.Queries) != 1 {
		t.Fatalf("got %d queries, want 1", len(f.Queries))
	}

	q := f.Queries[0]
	if q.Name != "CustomJoin" {
		t.Errorf("Name = %q, want %q", q.Name, "CustomJoin")
	}
	if !q.HasFilters {
		t.Error("expected HasFilters=true")
	}
	if q.Filters["conditions"] != "email,display_name" {
		t.Errorf("Filters[conditions] = %q, want %q", q.Filters["conditions"], "email,display_name")
	}
	if !strings.Contains(q.SQL, "LEFT JOIN") {
		t.Errorf("SQL missing LEFT JOIN: %s", q.SQL)
	}
}

func TestParseString_EmptyFile(t *testing.T) {
	f, err := ParseString("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.Queries) != 0 {
		t.Errorf("expected 0 queries, got %d", len(f.Queries))
	}
}

func TestParseString_Defaults(t *testing.T) {
	input := `-- @func: GetUser
SELECT * FROM users WHERE user_id = @user_id;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	if f.Database != "primary" {
		t.Errorf("Database = %q, want default %q", f.Database, "primary")
	}
}

func TestParseString_FileAnnotations(t *testing.T) {
	input := `-- @database: analytics
-- @parent: tenant_id

-- @func: GetStats
SELECT count(*) FROM events WHERE tenant_id = @tenant_id;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	if f.Database != "analytics" {
		t.Errorf("Database = %q, want %q", f.Database, "analytics")
	}
	if f.FileAnnotations["parent"] != "tenant_id" {
		t.Errorf("FileAnnotations[parent] = %q, want %q", f.FileAnnotations["parent"], "tenant_id")
	}
}

func TestParseString_SQLOutsideBlock(t *testing.T) {
	input := `SELECT * FROM users;`
	_, err := ParseString(input)
	if err == nil {
		t.Error("expected error for SQL outside query block")
	}
}

func TestParseString_MultipleNamedFilters(t *testing.T) {
	input := `-- @func: ListUsers
-- @filter:conditions *,-record_state
-- @filter:status record_state
-- @order: *,-record_state
-- @max: 100
SELECT *
FROM users
WHERE $conditions AND $status
ORDER BY $order
LIMIT $limit
;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}

	if len(f.Queries) != 1 {
		t.Fatalf("got %d queries, want 1", len(f.Queries))
	}

	q := f.Queries[0]
	if len(q.Filters) != 2 {
		t.Fatalf("got %d filters, want 2", len(q.Filters))
	}
	if q.Filters["conditions"] != "*,-record_state" {
		t.Errorf("Filters[conditions] = %q, want %q", q.Filters["conditions"], "*,-record_state")
	}
	if q.Filters["status"] != "record_state" {
		t.Errorf("Filters[status] = %q, want %q", q.Filters["status"], "record_state")
	}
	if !q.HasFilters {
		t.Error("expected HasFilters=true")
	}
}

func TestParseString_SearchSpec(t *testing.T) {
	input := `-- @func: ListUsers
-- @search: web_search(email, display_name)
-- @filter:conditions *
-- @order: *
-- @max: 100
SELECT *
FROM users
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	q := f.Queries[0]
	if q.SearchSpec != "web_search(email, display_name)" {
		t.Errorf("SearchSpec = %q, want %q", q.SearchSpec, "web_search(email, display_name)")
	}
	if !q.HasSearch {
		t.Error("expected HasSearch=true")
	}
}

func TestExtractParams(t *testing.T) {
	tests := []struct {
		sql    string
		params []string
	}{
		{"WHERE user_id = @user_id", []string{"user_id"}},
		{"WHERE user_id = @user_id AND email = @email", []string{"user_id", "email"}},
		{"WHERE $filters", nil},
		{"-- @annotation: value", nil},
		{"SET name = @name WHERE id = @id", []string{"name", "id"}},
	}
	for _, tt := range tests {
		got := extractParams(tt.sql)
		if len(got) != len(tt.params) {
			t.Errorf("extractParams(%q) = %v, want %v", tt.sql, got, tt.params)
			continue
		}
		for i := range got {
			if got[i] != tt.params[i] {
				t.Errorf("extractParams(%q)[%d] = %q, want %q", tt.sql, i, got[i], tt.params[i])
			}
		}
	}
}

func TestParseSearchAnnotation(t *testing.T) {
	tests := []struct {
		spec     string
		wantType string
		wantCols string
	}{
		{"ilike(email, name)", "ilike", "email, name"},
		{"web_search(title, body)", "web_search", "title, body"},
		{"tsvector(search_vector)", "tsvector", "search_vector"},
		{"email, name", "ilike", "email, name"},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			gotType, gotCols := parseSearchAnnotation(tt.spec)
			if gotType != tt.wantType {
				t.Errorf("searchType = %q, want %q", gotType, tt.wantType)
			}
			if gotCols != tt.wantCols {
				t.Errorf("columns = %q, want %q", gotCols, tt.wantCols)
			}
		})
	}
}

func TestParseAnnotation(t *testing.T) {
	tests := []struct {
		input    string
		wantKey  string
		wantName string
		wantVal  string
		wantOK   bool
	}{
		{"@func: ListUsers", "func", "", "ListUsers", true},
		{"@filter:conditions *,-record_state", "filter", "conditions", "*,-record_state", true},
		{"@filter:status record_state", "filter", "status", "record_state", true},
		{"@order: *,-record_state", "order", "", "*,-record_state", true},
		{"@max: 200", "max", "", "200", true},
		{"@search: ilike(email, display_name)", "search", "", "ilike(email, display_name)", true},
		{"@database: primary", "database", "", "primary", true},
		{"@filter:conditions", "filter", "conditions", "", true},
		{"not an annotation", "", "", "", false},
		{"@ : bad", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			key, name, val, ok := parseAnnotation(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if val != tt.wantVal {
				t.Errorf("val = %q, want %q", val, tt.wantVal)
			}
		})
	}
}

// ─── test helpers ────────────────────────────────────────────────────────────

func assertParams(t *testing.T, q QueryBlock, expected ...string) {
	t.Helper()
	if len(q.Params) != len(expected) {
		t.Errorf("%s: Params = %v, want %v", q.Name, q.Params, expected)
		return
	}
	for i, p := range expected {
		if q.Params[i] != p {
			t.Errorf("%s: Params[%d] = %q, want %q", q.Name, i, q.Params[i], p)
		}
	}
}

