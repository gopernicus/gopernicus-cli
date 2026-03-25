package generators

import (
	"testing"
)

// =============================================================================
// @auth.relations parsing
// =============================================================================

func TestParseAuthRelations(t *testing.T) {
	input := "owner(user, service_account), manager(user, service_account), member(user)"
	rels, err := ParseAuthRelations(input)
	if err != nil {
		t.Fatalf("ParseAuthRelations: %v", err)
	}

	if len(rels) != 3 {
		t.Fatalf("got %d relations, want 3", len(rels))
	}

	if rels[0].Name != "owner" {
		t.Errorf("rels[0].Name = %q, want %q", rels[0].Name, "owner")
	}
	if len(rels[0].Subjects) != 2 || rels[0].Subjects[0] != "user" || rels[0].Subjects[1] != "service_account" {
		t.Errorf("rels[0].Subjects = %v, want [user service_account]", rels[0].Subjects)
	}

	if rels[2].Name != "member" {
		t.Errorf("rels[2].Name = %q, want %q", rels[2].Name, "member")
	}
	if len(rels[2].Subjects) != 1 || rels[2].Subjects[0] != "user" {
		t.Errorf("rels[2].Subjects = %v, want [user]", rels[2].Subjects)
	}
}

func TestParseAuthRelations_WithFKRef(t *testing.T) {
	input := "owner(user, service_account), tenant(tenant)"
	rels, err := ParseAuthRelations(input)
	if err != nil {
		t.Fatalf("ParseAuthRelations: %v", err)
	}

	if len(rels) != 2 {
		t.Fatalf("got %d relations, want 2", len(rels))
	}
	if rels[1].Name != "tenant" || rels[1].Subjects[0] != "tenant" {
		t.Errorf("rels[1] = %+v, want tenant(tenant)", rels[1])
	}
}

func TestParseAuthRelations_Empty(t *testing.T) {
	rels, err := ParseAuthRelations("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relations, got %d", len(rels))
	}
}

// =============================================================================
// @auth.permissions parsing
// =============================================================================

func TestParseAuthPermissions(t *testing.T) {
	input := "create(authenticated), read(owner|manager|member), update(owner|manager), delete(owner)"
	perms, err := ParseAuthPermissions(input)
	if err != nil {
		t.Fatalf("ParseAuthPermissions: %v", err)
	}

	if len(perms) != 4 {
		t.Fatalf("got %d permissions, want 4", len(perms))
	}

	if perms[0].Name != "create" || len(perms[0].Rules) != 1 || perms[0].Rules[0] != "authenticated" {
		t.Errorf("perms[0] = %+v, want create(authenticated)", perms[0])
	}

	if perms[1].Name != "read" {
		t.Errorf("perms[1].Name = %q, want %q", perms[1].Name, "read")
	}
	if len(perms[1].Rules) != 3 {
		t.Fatalf("perms[1].Rules = %v, want 3 rules", perms[1].Rules)
	}
	if perms[1].Rules[0] != "owner" || perms[1].Rules[1] != "manager" || perms[1].Rules[2] != "member" {
		t.Errorf("perms[1].Rules = %v, want [owner manager member]", perms[1].Rules)
	}
}

func TestParseAuthPermissions_ThroughRules(t *testing.T) {
	input := "create(tenant->manage), read(owner|manager|tenant->member)"
	perms, err := ParseAuthPermissions(input)
	if err != nil {
		t.Fatalf("ParseAuthPermissions: %v", err)
	}

	if len(perms) != 2 {
		t.Fatalf("got %d permissions, want 2", len(perms))
	}

	if perms[0].Rules[0] != "tenant->manage" {
		t.Errorf("perms[0].Rules[0] = %q, want %q", perms[0].Rules[0], "tenant->manage")
	}

	if perms[1].Rules[2] != "tenant->member" {
		t.Errorf("perms[1].Rules[2] = %q, want %q", perms[1].Rules[2], "tenant->member")
	}
}

// =============================================================================
// @auth.create parsing
// =============================================================================

func TestParseAuthCreateRels(t *testing.T) {
	input := "tenant:{tenant_id}#owner@{subject}"
	rels, err := ParseAuthCreateRels(input)
	if err != nil {
		t.Fatalf("ParseAuthCreateRels: %v", err)
	}

	if len(rels) != 1 {
		t.Fatalf("got %d rels, want 1", len(rels))
	}

	r := rels[0]
	if r.ResourceType != "tenant" {
		t.Errorf("ResourceType = %q, want %q", r.ResourceType, "tenant")
	}
	if r.ResourceID != "{tenant_id}" {
		t.Errorf("ResourceID = %q, want %q", r.ResourceID, "{tenant_id}")
	}
	if r.Relation != "owner" {
		t.Errorf("Relation = %q, want %q", r.Relation, "owner")
	}
	if r.SubjectType != "{subject}" {
		t.Errorf("SubjectType = %q, want %q", r.SubjectType, "{subject}")
	}
	if r.SubjectID != "" {
		t.Errorf("SubjectID = %q, want empty", r.SubjectID)
	}
}

func TestParseAuthCreateRels_Multiple(t *testing.T) {
	input := "api_key:{api_key_id}#owner@{subject}, api_key:{api_key_id}#service_account@service_account:{service_account_id}"
	rels, err := ParseAuthCreateRels(input)
	if err != nil {
		t.Fatalf("ParseAuthCreateRels: %v", err)
	}

	if len(rels) != 2 {
		t.Fatalf("got %d rels, want 2", len(rels))
	}

	r := rels[1]
	if r.ResourceType != "api_key" {
		t.Errorf("ResourceType = %q, want %q", r.ResourceType, "api_key")
	}
	if r.Relation != "service_account" {
		t.Errorf("Relation = %q, want %q", r.Relation, "service_account")
	}
	if r.SubjectType != "service_account" {
		t.Errorf("SubjectType = %q, want %q", r.SubjectType, "service_account")
	}
	if r.SubjectID != "{service_account_id}" {
		t.Errorf("SubjectID = %q, want %q", r.SubjectID, "{service_account_id}")
	}
}

// =============================================================================
// Full queries.sql integration: legacy annotations now ignored
// =============================================================================

func TestParseString_LegacyAnnotationsIgnored(t *testing.T) {
	// Legacy @http:json, @authenticated, @auth.create, @auth.relation, @auth.permission
	// annotations are no longer parsed into dedicated fields. They end up in
	// FileAnnotations or query Annotations (as generic key/value pairs).
	input := `-- @database: primary

-- @func: List
-- @filter:conditions *
-- @order: *
-- @max: 100
SELECT * FROM tenants WHERE $conditions ORDER BY $order LIMIT $limit;

-- @func: Get
-- @cache: 5m
SELECT * FROM tenants WHERE tenant_id = @tenant_id;
`
	f, err := ParseString(input)
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}

	if len(f.Queries) != 2 {
		t.Fatalf("got %d queries, want 2", len(f.Queries))
	}

	list := f.Queries[0]
	if list.Name != "List" {
		t.Errorf("Name = %q, want %q", list.Name, "List")
	}
	if list.LimitSpec != "100" {
		t.Errorf("LimitSpec = %q, want %q", list.LimitSpec, "100")
	}

	get := f.Queries[1]
	if get.Name != "Get" {
		t.Errorf("Name = %q, want %q", get.Name, "Get")
	}
	if get.CacheSpec != "5m" {
		t.Errorf("CacheSpec = %q, want %q", get.CacheSpec, "5m")
	}
}
