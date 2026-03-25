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
