package queryfile

import (
	"testing"
)

// ─── New format tests ────────────────────────────────────────────────────────

const sampleNewFormat = `-- @database: primary

-- @func: ListUsers
-- @search: ilike(email, display_name)
SELECT *
FROM users
WHERE $filters -- *,-record_state
ORDER BY $order -- *,-record_state
LIMIT $lim -- 200
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

	// File-level headers.
	if f.Database != "primary" {
		t.Errorf("Database = %q, want %q", f.Database, "primary")
	}
	// Table is not parsed — set externally.
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
	if q.FilterSpec != "*,-record_state" {
		t.Errorf("ListUsers FilterSpec = %q, want %q", q.FilterSpec, "*,-record_state")
	}
	if q.OrderSpec != "*,-record_state" {
		t.Errorf("ListUsers OrderSpec = %q, want %q", q.OrderSpec, "*,-record_state")
	}
	if q.LimitSpec != "200" {
		t.Errorf("ListUsers LimitSpec = %q, want %q", q.LimitSpec, "200")
	}
	if q.Annotations["search"] != "ilike(email, display_name)" {
		t.Errorf("ListUsers @search = %q", q.Annotations["search"])
	}
	if !q.ReturnsRows {
		t.Error("ListUsers: expected ReturnsRows=true")
	}
	// SQL should not contain inline specs.
	if containsStr(q.SQL, "-- *,-record_state") {
		t.Errorf("ListUsers SQL should not contain inline specs: %s", q.SQL)
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
SELECT *
FROM widgets
WHERE $filters -- *
ORDER BY $order -- *
LIMIT $lim -- 100
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
	if q.FilterSpec != "*" {
		t.Errorf("FilterSpec = %q, want %q", q.FilterSpec, "*")
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
-- @search: ilike(email, display_name)
SELECT u.user_id, u.email, p.profile_id
    FROM users u
    LEFT JOIN profiles p ON u.user_id = p.profile_id
    WHERE $filters -- email,display_name
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
	if q.FilterSpec != "email,display_name" {
		t.Errorf("FilterSpec = %q, want %q", q.FilterSpec, "email,display_name")
	}
	// SQL should contain the JOIN.
	if !containsStr(q.SQL, "LEFT JOIN") {
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

// ─── extractInlineSpecs tests ────────────────────────────────────────────────

func TestExtractInlineSpecs(t *testing.T) {
	tests := []struct {
		name                                   string
		sql                                    string
		wantFilter, wantOrder, wantLimit       string
		wantContains, wantNotContains          string
	}{
		{
			name:           "all three specs",
			sql:            "SELECT *\nFROM users\nWHERE $filters -- *,-record_state\nORDER BY $order -- *,-record_state\nLIMIT $lim -- 200",
			wantFilter:     "*,-record_state",
			wantOrder:      "*,-record_state",
			wantLimit:      "200",
			wantContains:   "$filters",
			wantNotContains: "-- *,-record_state",
		},
		{
			name:       "no specs",
			sql:        "SELECT * FROM users WHERE user_id = @user_id",
			wantFilter: "",
			wantOrder:  "",
			wantLimit:  "",
		},
		{
			name:       "filter only",
			sql:        "WHERE $filters -- email,display_name",
			wantFilter: "email,display_name",
			wantOrder:  "",
			wantLimit:  "",
		},
		{
			name:       "wildcard only",
			sql:        "WHERE $filters -- *\nORDER BY $order -- *",
			wantFilter: "*",
			wantOrder:  "*",
			wantLimit:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, filter, order, limit := extractInlineSpecs(tt.sql)
			if filter != tt.wantFilter {
				t.Errorf("filterSpec = %q, want %q", filter, tt.wantFilter)
			}
			if order != tt.wantOrder {
				t.Errorf("orderSpec = %q, want %q", order, tt.wantOrder)
			}
			if limit != tt.wantLimit {
				t.Errorf("limitSpec = %q, want %q", limit, tt.wantLimit)
			}
			if tt.wantContains != "" && !containsStr(cleaned, tt.wantContains) {
				t.Errorf("cleaned SQL missing %q: %s", tt.wantContains, cleaned)
			}
			if tt.wantNotContains != "" && containsStr(cleaned, tt.wantNotContains) {
				t.Errorf("cleaned SQL should not contain %q: %s", tt.wantNotContains, cleaned)
			}
		})
	}
}

func TestExtractParams(t *testing.T) {
	tests := []struct {
		sql    string
		params []string
	}{
		{"WHERE user_id = @user_id", []string{"user_id"}},
		{"WHERE user_id = @user_id AND email = @email", []string{"user_id", "email"}},
		{"WHERE $filters", nil},                        // $filters is not a param
		{"-- @annotation: value", nil},                 // annotation, not param
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

// ─── extractSelectCols tests ─────────────────────────────────────────────────

func TestExtractSelectCols(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{"wildcard", "SELECT * FROM users WHERE user_id = @user_id", ""},
		{"explicit cols", "SELECT user_id, email FROM users WHERE email = @email", "user_id, email"},
		{"multiline explicit", "SELECT user_id, email\nFROM users\nWHERE email = @email", "user_id, email"},
		{"not a select", "INSERT INTO users ($fields) VALUES ($values)", ""},
		{"delete", "DELETE FROM users WHERE user_id = @user_id", ""},
		{"aliased cols", "SELECT u.user_id, u.email, p.profile_id\nFROM users u", "u.user_id, u.email, p.profile_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSelectCols(tt.sql)
			if got != tt.want {
				t.Errorf("extractSelectCols() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ─── extractReturningCols tests ──────────────────────────────────────────────

func TestExtractReturningCols(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want string
	}{
		{"returning star", "INSERT INTO users (name) VALUES (@name) RETURNING *", ""},
		{"returning explicit", "INSERT INTO users (name) VALUES (@name) RETURNING user_id, email", "user_id, email"},
		{"no returning", "DELETE FROM users WHERE user_id = @user_id", ""},
		{"update returning star", "UPDATE users SET name = @name WHERE user_id = @user_id RETURNING *", ""},
		{"update returning explicit", "UPDATE users SET name = @name WHERE user_id = @user_id RETURNING user_id, name", "user_id, name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractReturningCols(tt.sql)
			if got != tt.want {
				t.Errorf("extractReturningCols() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ─── parseSearchAnnotation tests ─────────────────────────────────────────────

func TestParseSearchAnnotation(t *testing.T) {
	tests := []struct {
		spec       string
		wantType   string
		wantCols   string
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

// ─── integration: new fields populated on QueryBlock ─────────────────────────

func TestParseString_SearchType(t *testing.T) {
	input := `-- @func: ListUsers
-- @search: web_search(email, display_name)
SELECT *
FROM users
WHERE $filters -- *
ORDER BY $order -- *
LIMIT $lim -- 200
;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	q := f.Queries[0]
	if q.SearchType != "web_search" {
		t.Errorf("SearchType = %q, want %q", q.SearchType, "web_search")
	}
}

func TestParseString_ExplicitSelectCols(t *testing.T) {
	input := `-- @func: GetUserEmail
SELECT user_id, email
FROM users
WHERE user_id = @user_id
;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	q := f.Queries[0]
	if q.SelectCols != "user_id, email" {
		t.Errorf("SelectCols = %q, want %q", q.SelectCols, "user_id, email")
	}
}

func TestParseString_ExplicitReturningCols(t *testing.T) {
	input := `-- @func: UpdateUser
-- @fields: *,-user_id,-created_at
UPDATE users
SET $fields
WHERE user_id = @user_id
RETURNING user_id, email;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	q := f.Queries[0]
	if q.ReturnCols != "user_id, email" {
		t.Errorf("ReturnCols = %q, want %q", q.ReturnCols, "user_id, email")
	}
}

func TestParseString_WildcardSelectHasEmptySelectCols(t *testing.T) {
	input := `-- @func: GetUser
SELECT * FROM users WHERE user_id = @user_id;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	q := f.Queries[0]
	if q.SelectCols != "" {
		t.Errorf("SelectCols = %q, want empty for SELECT *", q.SelectCols)
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

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
