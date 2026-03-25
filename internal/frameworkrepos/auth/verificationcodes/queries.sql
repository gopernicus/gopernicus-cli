-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(identifier, purpose)
-- @order: *
-- @max: 100
SELECT *
FROM verification_codes
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM verification_codes
WHERE code_id = @code_id
;

-- @func: Create
-- @fields: *,-created_at
INSERT INTO verification_codes
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-code_id,-created_at
UPDATE verification_codes
SET $fields
WHERE code_id = @code_id
RETURNING *;

-- @func: Delete
DELETE FROM verification_codes
WHERE code_id = @code_id
;

-- @func: GetByIdentifierAndPurpose
-- Looks up an active, non-expired verification code by identifier and purpose.
SELECT *
FROM verification_codes
WHERE identifier = @identifier
  AND purpose = @purpose
  AND expires_at > @now
;

-- @func: IncrementAttempts
-- Increments the failed attempt count. Returns the updated row.
-- @returns: attempt_count
UPDATE verification_codes
SET attempt_count = attempt_count + 1
WHERE identifier = @identifier
  AND purpose = @purpose
RETURNING attempt_count
;

-- @func: DeleteByIdentifierAndPurpose
-- Deletes a verification code after successful use or expiry.
DELETE FROM verification_codes
WHERE identifier = @identifier
  AND purpose = @purpose
;

