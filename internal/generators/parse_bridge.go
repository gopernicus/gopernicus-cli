package generators

import (
	"fmt"
	"strings"
)

// ParseAuthRelations parses an @auth.relations annotation value.
//
//	"owner(user, service_account), member(user, service_account), tenant(tenant)"
//	→ []AuthRelation{{Name: "owner", Subjects: ["user", "service_account"]}, ...}
func ParseAuthRelations(raw string) ([]AuthRelation, error) {
	if raw == "" {
		return nil, nil
	}

	var relations []AuthRelation
	// Split on ), which ends each relation definition.
	for _, chunk := range splitTopLevel(raw, ')') {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		// Remove leading comma from subsequent chunks.
		chunk = strings.TrimLeft(chunk, ", ")

		parenIdx := strings.IndexByte(chunk, '(')
		if parenIdx < 0 {
			return nil, fmt.Errorf("invalid relation %q: expected name(subjects)", chunk)
		}

		name := strings.TrimSpace(chunk[:parenIdx])
		subjectsStr := strings.TrimSpace(chunk[parenIdx+1:])

		var subjects []string
		for _, s := range strings.Split(subjectsStr, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				subjects = append(subjects, s)
			}
		}

		relations = append(relations, AuthRelation{
			Name:     name,
			Subjects: subjects,
		})
	}

	return relations, nil
}

// ParseAuthPermissions parses an @auth.permissions annotation value.
//
//	"read(owner|manager|member), update(owner|manager), delete(owner)"
//	→ []AuthPermission{{Name: "read", Rules: ["owner", "manager", "member"]}, ...}
func ParseAuthPermissions(raw string) ([]AuthPermission, error) {
	if raw == "" {
		return nil, nil
	}

	var permissions []AuthPermission
	for _, chunk := range splitTopLevel(raw, ')') {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		chunk = strings.TrimLeft(chunk, ", ")

		parenIdx := strings.IndexByte(chunk, '(')
		if parenIdx < 0 {
			return nil, fmt.Errorf("invalid permission %q: expected name(rules)", chunk)
		}

		name := strings.TrimSpace(chunk[:parenIdx])
		rulesStr := strings.TrimSpace(chunk[parenIdx+1:])

		var rules []string
		for _, r := range strings.Split(rulesStr, "|") {
			r = strings.TrimSpace(r)
			if r != "" {
				rules = append(rules, r)
			}
		}

		permissions = append(permissions, AuthPermission{
			Name:  name,
			Rules: rules,
		})
	}

	return permissions, nil
}

// ParseAuthCreateRels parses an @auth.create annotation value.
//
//	"tenant:{tenant_id}#owner@{subject}, tenant:{tenant_id}#member@user:{user_id}"
//	→ []AuthCreateRel{...}
func ParseAuthCreateRels(raw string) ([]AuthCreateRel, error) {
	if raw == "" {
		return nil, nil
	}

	var rels []AuthCreateRel
	for _, tuple := range strings.Split(raw, ",") {
		tuple = strings.TrimSpace(tuple)
		if tuple == "" {
			continue
		}

		rel, err := parseAuthTuple(tuple)
		if err != nil {
			return nil, err
		}
		rels = append(rels, rel)
	}

	return rels, nil
}

// parseAuthTuple parses a single relationship tuple like:
//
//	"tenant:{tenant_id}#owner@{subject}"
//	"api_key:{api_key_id}#service_account@service_account:{service_account_id}"
func parseAuthTuple(tuple string) (AuthCreateRel, error) {
	// Split on #: resource_type:resource_id # relation @ subject
	hashIdx := strings.IndexByte(tuple, '#')
	if hashIdx < 0 {
		return AuthCreateRel{}, fmt.Errorf("invalid auth tuple %q: missing #", tuple)
	}

	resource := tuple[:hashIdx]
	rest := tuple[hashIdx+1:]

	// Split on @: relation @ subject_type:subject_id
	atIdx := strings.IndexByte(rest, '@')
	if atIdx < 0 {
		return AuthCreateRel{}, fmt.Errorf("invalid auth tuple %q: missing @", tuple)
	}

	relation := rest[:atIdx]
	subject := rest[atIdx+1:]

	// Parse resource: "tenant:{tenant_id}"
	resourceType, resourceID := splitTypeID(resource)

	// Parse subject: "{subject}" or "service_account:{service_account_id}"
	subjectType, subjectID := splitTypeID(subject)

	return AuthCreateRel{
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Relation:     relation,
		SubjectType:  subjectType,
		SubjectID:    subjectID,
	}, nil
}

// splitTypeID splits "type:id" into its parts. If no colon, the whole string
// is treated as the type (for things like "{subject}").
func splitTypeID(s string) (typ, id string) {
	idx := strings.IndexByte(s, ':')
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx+1:]
}

// splitTopLevel splits a string on a delimiter, but only at the top level
// (not inside parentheses). Used to split "a(x,y), b(z)" on ')'.
func splitTopLevel(s string, delim byte) []string {
	var result []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 && s[i] == delim {
				result = append(result, s[start:i])
				start = i + 1
			}
		}
	}
	if start < len(s) {
		trailing := strings.TrimSpace(s[start:])
		if trailing != "" {
			result = append(result, trailing)
		}
	}
	return result
}
