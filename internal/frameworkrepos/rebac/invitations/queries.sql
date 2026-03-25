-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(resource_type, resource_id, relation, identifier, identifier_type, resolved_subject_id, invited_by, invitation_status)
-- @order: *
-- @max: 100
SELECT *
FROM invitations
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM invitations
WHERE invitation_id = @invitation_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO invitations
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-invitation_id,-record_state,-created_at
UPDATE invitations
SET $fields
WHERE invitation_id = @invitation_id
RETURNING *;

-- @func: SoftDelete
UPDATE invitations
SET record_state = 'deleted'
WHERE invitation_id = @invitation_id
;

-- @func: Archive
UPDATE invitations
SET record_state = 'archived'
WHERE invitation_id = @invitation_id
;

-- @func: Restore
UPDATE invitations
SET record_state = 'active'
WHERE invitation_id = @invitation_id
;

-- @func: Delete
DELETE FROM invitations
WHERE invitation_id = @invitation_id
;

-- @func: GetByToken
-- Used by the accept flow to look up a pending invitation by its token hash.
SELECT *
FROM invitations
WHERE token_hash = @token_hash
  AND invitation_status = 'pending'
  AND expires_at > @now
;

-- @func: ListByResource
-- Lists all invitations for a resource (e.g. show pending invitations for a tenant).
-- Used by resource owners to manage outstanding invitations.
-- @filter:conditions invitation_status,relation,auto_accept
-- @search: ilike(identifier)
-- @order: *
-- @max: 100
SELECT *
FROM invitations
WHERE resource_type = @resource_type
  AND resource_id = @resource_id
  AND $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: ListBySubject
-- Lists all invitations for an authenticated subject (by resolved_subject_id).
-- @filter:conditions resource_type,invitation_status,relation,auto_accept
-- @order: *
-- @max: 100
SELECT *
FROM invitations
WHERE resolved_subject_id = @resolved_subject_id
  AND $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: ListByIdentifier
-- Lists invitations for an identifier (email). Used during registration to
-- auto-accept invitations when a user verifies their email. Non-expired only.
-- @filter:conditions invitation_status,auto_accept
-- @order: *
-- @max: 100
SELECT *
FROM invitations
WHERE identifier = @identifier
  AND identifier_type = @identifier_type
  AND expires_at > @now
  AND $conditions
ORDER BY $order
LIMIT $limit
;

