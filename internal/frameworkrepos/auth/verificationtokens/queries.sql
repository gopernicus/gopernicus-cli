-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(purpose, identifier)
-- @order: *
-- @max: 100
SELECT *
FROM verification_tokens
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM verification_tokens
WHERE token_id = @token_id
;

-- @func: Create
-- @fields: *,-created_at
INSERT INTO verification_tokens
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-token_id,-created_at
UPDATE verification_tokens
SET $fields
WHERE token_id = @token_id
RETURNING *;

-- @func: Delete
DELETE FROM verification_tokens
WHERE token_id = @token_id
;

-- @func: GetByIdentifierAndPurpose
-- Looks up a non-expired verification token by identifier and purpose.
SELECT *
FROM verification_tokens
WHERE identifier = @identifier
  AND purpose = @purpose
  AND expires_at > @now
;

-- @func: DeleteByIdentifierAndPurpose
-- Deletes all tokens for an identifier+purpose pair (e.g. invalidate on re-send).
DELETE FROM verification_tokens
WHERE identifier = @identifier
  AND purpose = @purpose
;

-- @func: DeleteByUserIDAndPurpose
-- Deletes all tokens for a user+purpose pair (e.g. cleanup on password reset completion).
DELETE FROM verification_tokens
WHERE user_id = @user_id
  AND purpose = @purpose
;

