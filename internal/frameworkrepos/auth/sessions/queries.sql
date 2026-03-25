-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(user_agent, ip_address)
-- @order: *
-- @max: 100
SELECT *
FROM sessions
WHERE parent_user_id = @parent_user_id AND $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM sessions
WHERE session_id = @session_id AND parent_user_id = @parent_user_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO sessions
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-session_id,-parent_user_id,-created_at
UPDATE sessions
SET $fields
WHERE session_id = @session_id AND parent_user_id = @parent_user_id
RETURNING *;

-- @func: Delete
DELETE FROM sessions
WHERE session_id = @session_id AND parent_user_id = @parent_user_id
;

-- @func: GetByTokenHash
SELECT *
FROM sessions
WHERE session_token_hash = @session_token_hash
;

-- @func: GetByRefreshHash
SELECT *
FROM sessions
WHERE refresh_token_hash = @refresh_token_hash
;

-- @func: GetByPreviousRefreshHash
SELECT *
FROM sessions
WHERE previous_refresh_token_hash = @previous_refresh_token_hash
;

-- @func: DeleteAllForUser
DELETE FROM sessions
WHERE parent_user_id = @parent_user_id
;

-- @func: DeleteAllForUserExcept
DELETE FROM sessions
WHERE parent_user_id = @parent_user_id
  AND session_id != @session_id
;

-- @func: UpdateByID
-- @fields: *,-session_id,-parent_user_id,-created_at
UPDATE sessions
SET $fields
WHERE session_id = @session_id
RETURNING *;

-- @func: DeleteByID
DELETE FROM sessions
WHERE session_id = @session_id
;

