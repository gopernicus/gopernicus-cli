package generators

import (
	"testing"
)

func TestParseBridgeYMLBytes_Valid(t *testing.T) {
	yml := []byte(`
entity: Question
repo: questions/questions
domain: questions

auth_relations:
  - "tenant(tenant)"
  - "owner(user, service_account)"

auth_permissions:
  - "list(tenant->list)"
  - "read(owner|tenant->read)"

routes:
  - func: List
    path: /tenants/{tenant_id}/questions
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          pattern: prefilter
          permission: read

  - func: Get
    path: /questions/{question_id}
    with_permissions: true
    middleware:
      - authenticate: user
      - rate_limit
      - authorize:
          permission: read
          param: question_id

  - func: Create
    path: /tenants/{tenant_id}/questions
    auth_create:
      - "question:{question_id}#owner@{=subject}"
      - "question:{question_id}#tenant@tenant:{tenant_id}"
    middleware:
      - max_body_size: 5242880
      - authenticate: any
      - rate_limit
      - authorize:
          permission: create
          param: tenant_id

  - func: Update
    path: /questions/{question_id}
    middleware:
      - max_body_size: 1048576
      - authenticate: any
      - rate_limit
      - authorize:
          permission: update
          param: question_id

  - func: SoftDelete
    method: PUT
    path: /questions/{question_id}/delete
    middleware:
      - authenticate: any
      - rate_limit
      - authorize:
          permission: delete
          param: question_id
`)

	parsed, err := ParseBridgeYMLBytes(yml)
	if err != nil {
		t.Fatalf("ParseBridgeYMLBytes: %v", err)
	}

	if parsed.Entity != "Question" {
		t.Errorf("Entity = %q, want Question", parsed.Entity)
	}
	if parsed.Repo != "questions/questions" {
		t.Errorf("Repo = %q", parsed.Repo)
	}
	if len(parsed.AuthRelations) != 2 {
		t.Errorf("AuthRelations = %d, want 2", len(parsed.AuthRelations))
	}
	if len(parsed.Routes) != 5 {
		t.Fatalf("Routes = %d, want 5", len(parsed.Routes))
	}

	// List route — prefilter via middleware.
	list := parsed.Routes[0]
	if list.Func != "List" {
		t.Errorf("Routes[0].Func = %q", list.Func)
	}
	if len(list.Middleware) != 3 {
		t.Fatalf("Routes[0].Middleware = %d, want 3", len(list.Middleware))
	}
	if list.Middleware[0].Authenticate != "any" {
		t.Errorf("Routes[0].Middleware[0].Authenticate = %q, want any", list.Middleware[0].Authenticate)
	}
	if !list.Middleware[1].RateLimit {
		t.Error("Routes[0].Middleware[1] should be rate_limit")
	}
	if list.Middleware[2].Authorize == nil {
		t.Fatal("Routes[0].Middleware[2].Authorize should not be nil")
	}
	if list.Middleware[2].Authorize.Pattern != "prefilter" {
		t.Errorf("Routes[0].Middleware[2].Authorize.Pattern = %q, want prefilter", list.Middleware[2].Authorize.Pattern)
	}

	// Get route — user auth, with_permissions.
	get := parsed.Routes[1]
	if !get.WithPermissions {
		t.Error("Routes[1].WithPermissions should be true")
	}
	if get.Middleware[0].Authenticate != "user" {
		t.Errorf("Routes[1].Middleware[0].Authenticate = %q, want user", get.Middleware[0].Authenticate)
	}

	// Create route — max_body_size + auth_create.
	create := parsed.Routes[2]
	if create.Middleware[0].MaxBodySize != 5242880 {
		t.Errorf("Routes[2].Middleware[0].MaxBodySize = %d, want 5242880", create.Middleware[0].MaxBodySize)
	}
	if len(create.AuthCreate) != 2 {
		t.Fatalf("Routes[2].AuthCreate = %d, want 2", len(create.AuthCreate))
	}

	// SoftDelete — method override.
	softDelete := parsed.Routes[4]
	if softDelete.Method != "PUT" {
		t.Errorf("Routes[4].Method = %q, want PUT", softDelete.Method)
	}
}

func TestParseBridgeYMLBytes_RawMiddleware(t *testing.T) {
	yml := []byte(`
entity: Widget
repo: test/widgets

routes:
  - func: Get
    path: /widgets/{widget_id}
    middleware:
      - authenticate: any
      - 'myCustomMiddleware(b.log, "special")'
      - authorize:
          permission: read
          param: widget_id
`)

	parsed, err := ParseBridgeYMLBytes(yml)
	if err != nil {
		t.Fatalf("ParseBridgeYMLBytes: %v", err)
	}

	mw := parsed.Routes[0].Middleware
	if len(mw) != 3 {
		t.Fatalf("Middleware = %d, want 3", len(mw))
	}
	if mw[1].Raw != `myCustomMiddleware(b.log, "special")` {
		t.Errorf("Middleware[1].Raw = %q", mw[1].Raw)
	}
}

func TestParseBridgeYMLBytes_CheckWithoutParam(t *testing.T) {
	yml := []byte(`
entity: Widget
repo: test/widgets

routes:
  - func: Get
    path: /widgets/{widget_id}
    middleware:
      - authorize:
          permission: read
`)
	_, err := ParseBridgeYMLBytes(yml)
	if err == nil {
		t.Fatal("expected error for check without param")
	}
}

func TestParseBridgeYMLBytes_MissingEntity(t *testing.T) {
	yml := []byte(`
repo: questions/questions
routes:
  - func: List
    path: /questions
`)
	_, err := ParseBridgeYMLBytes(yml)
	if err == nil {
		t.Fatal("expected error for missing entity")
	}
}

func TestParseBridgeYMLBytes_InvalidAuthMode(t *testing.T) {
	yml := []byte(`
entity: Widget
repo: test/widgets
routes:
  - func: Get
    path: /widgets/{widget_id}
    middleware:
      - authenticate: invalid_mode
`)
	_, err := ParseBridgeYMLBytes(yml)
	if err == nil {
		t.Fatal("expected error for invalid authenticate mode")
	}
}

func TestParseBridgeYMLBytes_NoRoutes(t *testing.T) {
	yml := []byte(`
entity: Question
repo: questions/questions
`)
	parsed, err := ParseBridgeYMLBytes(yml)
	if err != nil {
		t.Fatalf("ParseBridgeYMLBytes: %v", err)
	}
	if len(parsed.Routes) != 0 {
		t.Errorf("Routes = %d, want 0", len(parsed.Routes))
	}
}

func TestParseCompactAuthRel(t *testing.T) {
	tests := []struct {
		input    string
		wantRes  string
		wantRel  string
		wantSubT string
		wantSubI string
		wantErr  bool
	}{
		{
			input:    "question:{question_id}#owner@{=subject}",
			wantRes:  "question",
			wantRel:  "owner",
			wantSubT: "{=subject}",
		},
		{
			input:    "question:{question_id}#tenant@tenant:{tenant_id}",
			wantRes:  "question",
			wantRel:  "tenant",
			wantSubT: "tenant",
			wantSubI: "{tenant_id}",
		},
		{
			input:   "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		rel, err := parseCompactAuthRel(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseCompactAuthRel(%q): err = %v, wantErr = %v", tt.input, err, tt.wantErr)
			continue
		}
		if tt.wantErr {
			continue
		}
		if rel.ResourceType != tt.wantRes {
			t.Errorf("parseCompactAuthRel(%q): ResourceType = %q, want %q", tt.input, rel.ResourceType, tt.wantRes)
		}
		if rel.Relation != tt.wantRel {
			t.Errorf("parseCompactAuthRel(%q): Relation = %q, want %q", tt.input, rel.Relation, tt.wantRel)
		}
		if rel.SubjectType != tt.wantSubT {
			t.Errorf("parseCompactAuthRel(%q): SubjectType = %q, want %q", tt.input, rel.SubjectType, tt.wantSubT)
		}
		if rel.SubjectID != tt.wantSubI {
			t.Errorf("parseCompactAuthRel(%q): SubjectID = %q, want %q", tt.input, rel.SubjectID, tt.wantSubI)
		}
	}
}

// TestReorderPathParamsForRepo_FiltersNonQueryParams asserts that path params
// not referenced by the SQL query's named-param set are dropped, not appended.
// Regression: previously, URL scoping segments like {space_id} leaked into the
// repo call argument list when the query's WHERE clause only used {tenant_id}
// and {dashboard_id}, producing "too many arguments" compile errors in the
// generated bridge.
func TestReorderPathParamsForRepo_FiltersNonQueryParams(t *testing.T) {
	pathParams := []PathParam{
		{Name: "tenant_id", GoName: "tenantID"},
		{Name: "space_id", GoName: "spaceID"},
		{Name: "dashboard_id", GoName: "dashboardID"},
		{Name: "assignment_id", GoName: "assignmentID"},
	}
	queryParams := []string{"tenant_id", "dashboard_id"}

	got := reorderPathParamsForRepo(pathParams, queryParams)

	if len(got) != 2 {
		t.Fatalf("expected 2 repo call params, got %d: %+v", len(got), got)
	}
	if got[0].Name != "tenant_id" || got[1].Name != "dashboard_id" {
		t.Errorf("unexpected order/content: %+v", got)
	}
	for _, p := range got {
		if p.Name == "space_id" || p.Name == "assignment_id" {
			t.Errorf("non-query param %q leaked into repo call args", p.Name)
		}
	}
}

// TestReorderPathParamsForRepo_FollowsQueryOrder ensures the reorder still
// matches SQL @param declaration order even when URL order differs.
func TestReorderPathParamsForRepo_FollowsQueryOrder(t *testing.T) {
	pathParams := []PathParam{
		{Name: "tenant_id"},
		{Name: "dashboard_id"},
	}
	queryParams := []string{"dashboard_id", "tenant_id"}

	got := reorderPathParamsForRepo(pathParams, queryParams)

	if len(got) != 2 || got[0].Name != "dashboard_id" || got[1].Name != "tenant_id" {
		t.Errorf("expected SQL-ordered [dashboard_id, tenant_id], got %+v", got)
	}
}

// TestComputeHandlerPathParams_UnionCoverage asserts that locals needed by
// params_to_input, repo-call args, or delete-cleanup are kept, and orphans
// are dropped so the handler doesn't emit "declared and not used".
func TestComputeHandlerPathParams_UnionCoverage(t *testing.T) {
	pathParams := []PathParam{
		{Name: "tenant_id"},
		{Name: "parent_space_id"},
		{Name: "unused_scope"},
		{Name: "space_id"},
	}
	repoCall := []PathParam{{Name: "tenant_id"}}
	paramsToInput := []PathParam{{Name: "parent_space_id"}}

	got := computeHandlerPathParams(pathParams, repoCall, paramsToInput, true, "space_id")

	want := map[string]bool{
		"tenant_id":       true, // repo call
		"parent_space_id": true, // params_to_input
		"space_id":        true, // delete cleanup PK
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d params, got %d: %+v", len(want), len(got), got)
	}
	for _, p := range got {
		if !want[p.Name] {
			t.Errorf("unexpected param %q in handler extraction set", p.Name)
		}
	}
	for _, p := range got {
		if p.Name == "unused_scope" {
			t.Error("orphan path param was not dropped")
		}
	}
}

// TestComputeHandlerPathParams_PreservesURLOrder ensures the returned slice
// keeps the original URL order for readable handler output.
func TestComputeHandlerPathParams_PreservesURLOrder(t *testing.T) {
	pathParams := []PathParam{
		{Name: "tenant_id"},
		{Name: "parent_space_id"},
		{Name: "space_id"},
	}
	repoCall := []PathParam{{Name: "space_id"}, {Name: "tenant_id"}}
	paramsToInput := []PathParam{{Name: "parent_space_id"}}

	got := computeHandlerPathParams(pathParams, repoCall, paramsToInput, false, "")

	if len(got) != 3 {
		t.Fatalf("expected 3, got %d: %+v", len(got), got)
	}
	if got[0].Name != "tenant_id" || got[1].Name != "parent_space_id" || got[2].Name != "space_id" {
		t.Errorf("expected URL-ordered result, got %+v", got)
	}
}

// TestParamsToInputTargetFields_CoversInsertAndSet asserts that the helper
// used to detect pointer target fields reads both InsertFields (for create)
// and SetFields (for update), so a pointer FK on an update path is also
// wrapped with & in the generated assignment.
func TestParamsToInputTargetFields_CoversInsertAndSet(t *testing.T) {
	rq := ResolvedQuery{
		InsertFields: []FieldInfo{
			{DBName: "name", GoType: "string"},
			{DBName: "parent_space_id", GoType: "*string"},
		},
		SetFields: []FieldInfo{
			{DBName: "description", GoType: "*string"},
		},
	}

	got := paramsToInputTargetFields(rq)

	if got["parent_space_id"] != "*string" {
		t.Errorf("expected parent_space_id *string, got %q", got["parent_space_id"])
	}
	if got["description"] != "*string" {
		t.Errorf("expected description *string, got %q", got["description"])
	}
	if got["name"] != "string" {
		t.Errorf("expected name string, got %q", got["name"])
	}
}
