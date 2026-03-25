-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(email, display_name)
-- @order: *
-- @max: 100
SELECT *
FROM users
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM users
WHERE user_id = @user_id
;

-- Create is handled by a custom store method (transactional principal + user insert).
-- See userspgx/store.go.

-- @func: Update
-- @fields: *,-user_id,-record_state,-created_at
UPDATE users
SET $fields
WHERE user_id = @user_id
RETURNING *;

-- @func: SoftDelete
UPDATE users
SET record_state = 'deleted'
WHERE user_id = @user_id
;

-- @func: Archive
UPDATE users
SET record_state = 'archived'
WHERE user_id = @user_id
;

-- @func: Restore
UPDATE users
SET record_state = 'active'
WHERE user_id = @user_id
;

-- @func: Delete
DELETE FROM users
WHERE user_id = @user_id
;

-- @func: GetByEmail
SELECT *
FROM users
WHERE email = @email
;

-- @func: SetEmailVerified
UPDATE users
SET email_verified = true, updated_at = @updated_at
WHERE user_id = @user_id
;

-- @func: SetLastLogin
UPDATE users
SET last_login_at = @last_login_at, updated_at = @updated_at
WHERE user_id = @user_id
;

