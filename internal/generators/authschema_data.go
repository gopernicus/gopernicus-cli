package generators

import (
	"sort"
	"strings"
)

// AuthSchemaEntity represents a single resource type in the authorization schema.
type AuthSchemaEntity struct {
	ResourceType string                  // singular lowercase resource name, e.g. "user"
	Relations    []AuthSchemaRelation    // sorted by name
	Permissions  []AuthSchemaPermission  // sorted by name
}

// AuthSchemaRelation defines what subjects can hold a relation.
type AuthSchemaRelation struct {
	Name            string                  // e.g. "owner", "member"
	AllowedSubjects []AuthSchemaSubjectRef  // who can hold this relation
}

// AuthSchemaSubjectRef references a subject type, optionally with a relation.
type AuthSchemaSubjectRef struct {
	Type     string // e.g. "user", "group"
	Relation string // optional: "member" for "group#member"
}

// AuthSchemaPermission defines how a permission is computed.
type AuthSchemaPermission struct {
	Name   string                 // e.g. "read", "create"
	Checks []AuthSchemaPermCheck  // OR'd checks
}

// AuthSchemaPermCheck is a single check in a permission rule.
type AuthSchemaPermCheck struct {
	IsDirect   bool   // true = Direct(Relation), false = Through(Relation, Permission)
	Relation   string // direct relation or traversal relation
	Permission string // only for through-checks
}

// BuildAuthSchemaEntities converts resolved queries files into auth schema
// entities ready for template rendering. Only entities with @auth.relation or
// @auth.permission annotations produce output.
func BuildAuthSchemaEntities(resolvedFiles []*ResolvedFile) []AuthSchemaEntity {
	// Sort for deterministic output.
	sorted := make([]*ResolvedFile, len(resolvedFiles))
	copy(sorted, resolvedFiles)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].PackageName < sorted[j].PackageName
	})

	var entities []AuthSchemaEntity
	for _, rf := range sorted {
		if len(rf.AuthRelations) == 0 && len(rf.AuthPermissions) == 0 {
			continue
		}
		entity := buildAuthSchemaEntityFromResolved(rf)
		entities = append(entities, entity)
	}

	return entities
}

func buildAuthSchemaEntityFromResolved(rf *ResolvedFile) AuthSchemaEntity {
	// Resource type: singularized table name (e.g. "api_keys" → "api_key").
	resourceType := Singularize(rf.TableName)
	if resourceType == "" {
		resourceType = Singularize(rf.PackageName)
	}

	entity := AuthSchemaEntity{
		ResourceType: resourceType,
	}

	// Build relations from @auth.relation annotations.
	for _, rel := range rf.AuthRelations {
		schemaRel := AuthSchemaRelation{
			Name: rel.Name,
		}
		for _, subject := range rel.Subjects {
			schemaRel.AllowedSubjects = append(schemaRel.AllowedSubjects, parseSubjectRef(subject))
		}
		entity.Relations = append(entity.Relations, schemaRel)
	}

	// Build permissions from @auth.permission annotations.
	for _, perm := range rf.AuthPermissions {
		schemaPerm := AuthSchemaPermission{
			Name: perm.Name,
		}
		for _, rule := range perm.Rules {
			schemaPerm.Checks = append(schemaPerm.Checks, parsePermissionCheck(rule))
		}
		entity.Permissions = append(entity.Permissions, schemaPerm)
	}

	return entity
}

// parseSubjectRef parses a subject reference string.
//   - "user"          → Type: "user"
//   - "group#member"  → Type: "group", Relation: "member"
func parseSubjectRef(s string) AuthSchemaSubjectRef {
	if idx := strings.IndexByte(s, '#'); idx >= 0 {
		return AuthSchemaSubjectRef{
			Type:     s[:idx],
			Relation: s[idx+1:],
		}
	}
	return AuthSchemaSubjectRef{Type: s}
}

// BuildAuthSchemaEntityFromBridgeYML creates an AuthSchemaEntity from a bridge.yml file.
// Used in flat generation where auth schema lives in bridge.yml, not queries.sql.
func BuildAuthSchemaEntityFromBridgeYML(yml *BridgeYML, tableName string) *AuthSchemaEntity {
	if len(yml.AuthRelations) == 0 && len(yml.AuthPermissions) == 0 {
		return nil
	}

	resourceType := Singularize(tableName)
	entity := AuthSchemaEntity{
		ResourceType: resourceType,
	}

	// Parse relations from compact string format: "owner(user, service_account)"
	for _, relStr := range yml.AuthRelations {
		rel := parseAuthRelationString(relStr)
		entity.Relations = append(entity.Relations, rel)
	}

	// Parse permissions from compact string format: "read(owner|tenant->read)"
	for _, permStr := range yml.AuthPermissions {
		perm := parseAuthPermissionString(permStr)
		entity.Permissions = append(entity.Permissions, perm)
	}

	return &entity
}

// parseAuthRelationString parses "name(subject1, subject2)" into an AuthSchemaRelation.
func parseAuthRelationString(s string) AuthSchemaRelation {
	// Split "owner(user, service_account)" → name="owner", subjects="user, service_account"
	idx := strings.IndexByte(s, '(')
	if idx < 0 {
		return AuthSchemaRelation{Name: s}
	}
	name := s[:idx]
	subjectsStr := strings.TrimSuffix(s[idx+1:], ")")

	var subjects []AuthSchemaSubjectRef
	for _, sub := range strings.Split(subjectsStr, ",") {
		sub = strings.TrimSpace(sub)
		if sub != "" {
			subjects = append(subjects, parseSubjectRef(sub))
		}
	}

	return AuthSchemaRelation{Name: name, AllowedSubjects: subjects}
}

// parseAuthPermissionString parses "name(rule1|rule2)" into an AuthSchemaPermission.
func parseAuthPermissionString(s string) AuthSchemaPermission {
	// Split "read(owner|tenant->read)" → name="read", rules="owner|tenant->read"
	idx := strings.IndexByte(s, '(')
	if idx < 0 {
		return AuthSchemaPermission{Name: s}
	}
	name := s[:idx]
	rulesStr := strings.TrimSuffix(s[idx+1:], ")")

	var checks []AuthSchemaPermCheck
	for _, rule := range strings.Split(rulesStr, "|") {
		rule = strings.TrimSpace(rule)
		if rule != "" {
			checks = append(checks, parsePermissionCheck(rule))
		}
	}

	return AuthSchemaPermission{Name: name, Checks: checks}
}

// parsePermissionCheck parses a permission rule string.
//   - "owner"          → Direct("owner")
//   - "tenant->manage" → Through("tenant", "manage")
func parsePermissionCheck(rule string) AuthSchemaPermCheck {
	if idx := strings.Index(rule, "->"); idx >= 0 {
		return AuthSchemaPermCheck{
			IsDirect:   false,
			Relation:   rule[:idx],
			Permission: rule[idx+2:],
		}
	}
	return AuthSchemaPermCheck{
		IsDirect: true,
		Relation: rule,
	}
}
