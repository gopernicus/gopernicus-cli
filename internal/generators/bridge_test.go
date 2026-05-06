package generators

import (
	"testing"
)

// TestCreateFieldSignature_DedupsIdenticalFieldSets asserts that two create
// queries with the same field set (after params_to_input stripping) collapse
// to the same signature, preventing duplicate DTO emission.
func TestCreateFieldSignature_DedupsIdenticalFieldSets(t *testing.T) {
	a := []BridgeField{
		{DBName: "name", GoType: "string"},
		{DBName: "slug", GoType: "string"},
	}
	b := []BridgeField{
		{DBName: "slug", GoType: "string"},
		{DBName: "name", GoType: "string"},
	}

	if createFieldSignature(a) != createFieldSignature(b) {
		t.Errorf("field sets with same members (different order) should produce equal signatures; got %q vs %q",
			createFieldSignature(a), createFieldSignature(b))
	}
}

// TestCreateFieldSignature_SeparatesDivergentSets asserts that two create
// queries with genuinely different field sets produce distinct signatures.
func TestCreateFieldSignature_SeparatesDivergentSets(t *testing.T) {
	a := []BridgeField{
		{DBName: "name", GoType: "string"},
	}
	b := []BridgeField{
		{DBName: "name", GoType: "string"},
		{DBName: "parent_id", GoType: "string"},
	}

	if createFieldSignature(a) == createFieldSignature(b) {
		t.Errorf("different field sets must produce different signatures")
	}
}

// TestResolveBridgeCreateRels_PointerFieldUnwrap asserts that when an
// auth_create placeholder resolves to a record field whose Go type is a
// pointer (e.g. a nullable self-referential parent FK), the emitted Go
// expression dereferences the pointer so it can be assigned to the
// authorization tuple's string field without a type mismatch.
func TestResolveBridgeCreateRels_PointerFieldUnwrap(t *testing.T) {
	rels := []AuthCreateRel{
		{
			ResourceType: "space",
			ResourceID:   "{space_id}",
			Relation:     "parent",
			SubjectType:  "space",
			SubjectID:    "{parent_space_id}",
		},
	}
	fieldTypes := map[string]string{
		"space_id":        "string",
		"parent_space_id": "*string",
	}

	got := resolveBridgeCreateRels(rels, fieldTypes)

	if len(got) != 1 {
		t.Fatalf("expected 1 rel, got %d", len(got))
	}
	if got[0].ResourceIDExpr != "record.SpaceID" {
		t.Errorf("non-pointer resource ID should not be deref'd; got %q", got[0].ResourceIDExpr)
	}
	if got[0].SubjectIDExpr != "*record.ParentSpaceID" {
		t.Errorf("pointer field subject ID must be deref'd; got %q", got[0].SubjectIDExpr)
	}
}

// TestResolveBridgeCreateRels_ContextSubject asserts the {=subject} flag
// keeps SubjectFromContext semantics intact regardless of the field-type map.
func TestResolveBridgeCreateRels_ContextSubject(t *testing.T) {
	rels := []AuthCreateRel{
		{
			ResourceType: "space",
			ResourceID:   "{space_id}",
			Relation:     "owner",
			SubjectType:  "{=subject}",
		},
	}

	got := resolveBridgeCreateRels(rels, map[string]string{"space_id": "string"})

	if len(got) != 1 {
		t.Fatalf("expected 1 rel")
	}
	if !got[0].SubjectFromContext {
		t.Error("expected SubjectFromContext=true for {=subject}")
	}
	if got[0].SubjectIDExpr != "" {
		t.Errorf("context subject should not have SubjectIDExpr set, got %q", got[0].SubjectIDExpr)
	}
}
