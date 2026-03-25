-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(resource_type, resource_id, relation, subject_type, subject_id, subject_relation)
-- @order: *
-- @max: 100
SELECT *
FROM rebac_relationships
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM rebac_relationships
WHERE relationship_id = @relationship_id
;

-- @func: Create
-- @fields: *,-created_at
INSERT INTO rebac_relationships
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-relationship_id,-created_at
UPDATE rebac_relationships
SET $fields
WHERE relationship_id = @relationship_id
RETURNING *;

-- @func: Delete
DELETE FROM rebac_relationships
WHERE relationship_id = @relationship_id
;

-- @func: ListBySubject
-- Returns all relationships where the subject matches, with optional filtering by resource_type or relation.
-- Used to enumerate what resources a subject has access to (e.g. "what tenants is this user a member of?").
-- @filter:conditions resource_type,relation
-- @order: *
-- @max: 100
SELECT *
FROM rebac_relationships
WHERE subject_type = @subject_type
  AND subject_id = @subject_id
  AND $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: ListByResource
-- Returns all relationships for a given resource, with optional filtering by subject_type or relation.
-- Used to enumerate who has access to a resource (e.g. "who are the members of this tenant?").
-- @filter:conditions subject_type,relation
-- @order: *
-- @max: 100
SELECT *
FROM rebac_relationships
WHERE resource_type = @resource_type
  AND resource_id = @resource_id
  AND $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: DeleteAllForResource
-- Hard-deletes all relationships for a resource (called on resource deletion).
DELETE FROM rebac_relationships
WHERE resource_type = @resource_type
  AND resource_id = @resource_id
;

-- @func: DeleteByTuple
-- Hard-deletes the specific relationship tuple.
DELETE FROM rebac_relationships
WHERE resource_type = @resource_type
  AND resource_id = @resource_id
  AND relation = @relation
  AND subject_type = @subject_type
  AND subject_id = @subject_id
;

-- @func: DeleteByResourceAndSubject
-- Removes all relations a subject holds on a specific resource (e.g., unassign all roles from a user on a resource).
DELETE FROM rebac_relationships
WHERE resource_type = @resource_type
  AND resource_id = @resource_id
  AND subject_type = @subject_type
  AND subject_id = @subject_id
;

-- @func: DeleteBySubject
-- Removes all relationships for a subject across all resources (e.g., when a user is deleted from the system).
DELETE FROM rebac_relationships
WHERE subject_type = @subject_type
  AND subject_id = @subject_id
;

