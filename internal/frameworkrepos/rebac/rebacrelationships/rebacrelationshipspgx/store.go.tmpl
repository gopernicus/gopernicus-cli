// This file is created once by gopernicus and will NOT be overwritten.
// Add custom store methods here. Store is defined in generated.go.

package rebacrelationshipspgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gopernicus/gopernicus/core/repositories/rebac/rebacrelationships"
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
)

// =============================================================================
// Authorization Check Queries
// =============================================================================

// groupExpansionCTE is a recursive CTE that expands a subject into all groups
// they belong to (directly or transitively via nested group membership).
const groupExpansionCTE = `
WITH RECURSIVE subject_groups AS (
	SELECT @subject_type::varchar AS subject_type, @subject_id::varchar AS subject_id
	UNION
	SELECT r.resource_type, r.resource_id
	FROM rebac_relationships r
	INNER JOIN subject_groups sg ON r.subject_type = sg.subject_type AND r.subject_id = sg.subject_id
	WHERE r.relation = 'member'
)`

// CheckRelationWithGroupExpansion checks if a subject has a relation to a resource,
// including indirect relations via group membership (recursive).
func (s *Store) CheckRelationWithGroupExpansion(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	query := groupExpansionCTE + `
SELECT EXISTS (
	SELECT 1 FROM rebac_relationships r
	INNER JOIN subject_groups sg ON r.subject_type = sg.subject_type AND r.subject_id = sg.subject_id
	WHERE r.resource_type = @resource_type
		AND r.resource_id = @resource_id
		AND r.relation = @relation
)`

	args := pgx.NamedArgs{
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"relation":      relation,
		"subject_type":  subjectType,
		"subject_id":    subjectID,
	}

	var exists bool
	if err := s.db.QueryRow(ctx, query, args).Scan(&exists); err != nil {
		return false, pgxdb.HandlePgError(err)
	}
	return exists, nil
}

// CheckRelationExists checks if a specific relationship tuple exists (no group expansion).
func (s *Store) CheckRelationExists(ctx context.Context, resourceType, resourceID, relation, subjectType, subjectID string) (bool, error) {
	query := `SELECT EXISTS (
	SELECT 1 FROM rebac_relationships
	WHERE resource_type = @resource_type
		AND resource_id = @resource_id
		AND relation = @relation
		AND subject_type = @subject_type
		AND subject_id = @subject_id
)`

	args := pgx.NamedArgs{
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"relation":      relation,
		"subject_type":  subjectType,
		"subject_id":    subjectID,
	}

	var exists bool
	if err := s.db.QueryRow(ctx, query, args).Scan(&exists); err != nil {
		return false, pgxdb.HandlePgError(err)
	}
	return exists, nil
}

// CheckBatchDirect performs a batch permission check across multiple resource IDs
// with group expansion. Returns a map of resourceID -> allowed.
func (s *Store) CheckBatchDirect(ctx context.Context, resourceType string, resourceIDs []string, relation, subjectType, subjectID string) (map[string]bool, error) {
	query := groupExpansionCTE + `
SELECT DISTINCT r.resource_id
FROM rebac_relationships r
INNER JOIN subject_groups sg ON r.subject_type = sg.subject_type AND r.subject_id = sg.subject_id
WHERE r.resource_type = @resource_type
	AND r.resource_id = ANY(@resource_ids)
	AND r.relation = @relation`

	args := pgx.NamedArgs{
		"resource_type": resourceType,
		"resource_ids":  resourceIDs,
		"relation":      relation,
		"subject_type":  subjectType,
		"subject_id":    subjectID,
	}

	rows, err := s.db.Query(ctx, query, args)
	if err != nil {
		return nil, pgxdb.HandlePgError(err)
	}
	defer rows.Close()

	result := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		result[id] = false
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, pgxdb.HandlePgError(err)
		}
		result[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.HandlePgError(err)
	}
	return result, nil
}

// GetRelationTargets returns all subjects that have a specific relation to a resource.
func (s *Store) GetRelationTargets(ctx context.Context, resourceType, resourceID, relation string) ([]rebacrelationships.RebacRelationship, error) {
	query := `SELECT relationship_id, resource_type, resource_id, relation,
		subject_type, subject_id, subject_relation, created_at
FROM rebac_relationships
WHERE resource_type = @resource_type
	AND resource_id = @resource_id
	AND relation = @relation`

	args := pgx.NamedArgs{
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"relation":      relation,
	}

	rows, err := s.db.Query(ctx, query, args)
	if err != nil {
		return nil, pgxdb.HandlePgError(err)
	}
	defer rows.Close()

	return pgx.CollectRows(rows, pgx.RowToStructByName[rebacrelationships.RebacRelationship])
}

// CountByResourceAndRelation counts relationships for a specific resource and relation.
func (s *Store) CountByResourceAndRelation(ctx context.Context, resourceType, resourceID, relation string) (int, error) {
	query := `SELECT COUNT(*) FROM rebac_relationships
WHERE resource_type = @resource_type
	AND resource_id = @resource_id
	AND relation = @relation`

	args := pgx.NamedArgs{
		"resource_type": resourceType,
		"resource_id":   resourceID,
		"relation":      relation,
	}

	var count int
	if err := s.db.QueryRow(ctx, query, args).Scan(&count); err != nil {
		return 0, pgxdb.HandlePgError(err)
	}
	return count, nil
}

// =============================================================================
// Authorization Batch Operations
// =============================================================================

// BulkCreate creates multiple relationships in a batch with ON CONFLICT DO NOTHING.
func (s *Store) BulkCreate(ctx context.Context, inputs []rebacrelationships.CreateRebacRelationship) ([]rebacrelationships.RebacRelationship, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	now := time.Now().UTC()

	ids := make([]string, len(inputs))
	resourceTypes := make([]string, len(inputs))
	resourceIDs := make([]string, len(inputs))
	relations := make([]string, len(inputs))
	subjectTypes := make([]string, len(inputs))
	subjectIDs := make([]string, len(inputs))
	subjectRelations := make([]*string, len(inputs))
	createdAts := make([]time.Time, len(inputs))

	for i, input := range inputs {
		ids[i] = input.RelationshipID
		resourceTypes[i] = input.ResourceType
		resourceIDs[i] = input.ResourceID
		relations[i] = input.Relation
		subjectTypes[i] = input.SubjectType
		subjectIDs[i] = input.SubjectID
		subjectRelations[i] = input.SubjectRelation
		createdAts[i] = now
	}

	query := `INSERT INTO rebac_relationships (
	relationship_id, resource_type, resource_id, relation,
	subject_type, subject_id, subject_relation, created_at
)
SELECT * FROM UNNEST(
	@relationship_ids::varchar[],
	@resource_types::varchar[],
	@resource_ids::varchar[],
	@relations::varchar[],
	@subject_types::varchar[],
	@subject_ids::varchar[],
	@subject_relations::varchar[],
	@created_ats::timestamptz[]
)
ON CONFLICT DO NOTHING
RETURNING relationship_id, resource_type, resource_id, relation,
          subject_type, subject_id, subject_relation, created_at`

	args := pgx.NamedArgs{
		"relationship_ids":  ids,
		"resource_types":    resourceTypes,
		"resource_ids":      resourceIDs,
		"relations":         relations,
		"subject_types":     subjectTypes,
		"subject_ids":       subjectIDs,
		"subject_relations": subjectRelations,
		"created_ats":       createdAts,
	}

	rows, err := s.db.Query(ctx, query, args)
	if err != nil {
		return nil, s.mapError(err)
	}
	defer rows.Close()

	return pgx.CollectRows(rows, pgx.RowToStructByName[rebacrelationships.RebacRelationship])
}

// =============================================================================
// LookupResources Queries
// =============================================================================

// LookupResourceIDs returns all resource IDs of a given type where the subject
// has any of the specified relations (with group expansion).
func (s *Store) LookupResourceIDs(ctx context.Context, resourceType string, relations []string, subjectType, subjectID string) ([]string, error) {
	query := groupExpansionCTE + `
SELECT DISTINCT r.resource_id
FROM rebac_relationships r
INNER JOIN subject_groups sg ON r.subject_type = sg.subject_type AND r.subject_id = sg.subject_id
WHERE r.resource_type = @resource_type
	AND r.relation = ANY(@relations)`

	args := pgx.NamedArgs{
		"resource_type": resourceType,
		"relations":     relations,
		"subject_type":  subjectType,
		"subject_id":    subjectID,
	}

	rows, err := s.db.Query(ctx, query, args)
	if err != nil {
		return nil, pgxdb.HandlePgError(err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, pgxdb.HandlePgError(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.HandlePgError(err)
	}
	return ids, nil
}

// LookupResourceIDsByRelationTarget returns resource IDs that have a specific
// relation pointing to any of the given target IDs.
func (s *Store) LookupResourceIDsByRelationTarget(ctx context.Context, resourceType, relation, targetType string, targetIDs []string) ([]string, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}

	query := `SELECT DISTINCT resource_id
FROM rebac_relationships
WHERE resource_type = @resource_type
	AND relation = @relation
	AND subject_type = @target_type
	AND subject_id = ANY(@target_ids)`

	args := pgx.NamedArgs{
		"resource_type": resourceType,
		"relation":      relation,
		"target_type":   targetType,
		"target_ids":    targetIDs,
	}

	rows, err := s.db.Query(ctx, query, args)
	if err != nil {
		return nil, pgxdb.HandlePgError(err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, pgxdb.HandlePgError(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, pgxdb.HandlePgError(err)
	}
	return ids, nil
}
