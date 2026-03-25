-- @database: primary

-- @func: List
-- @filter:conditions *
-- @order: *
-- @max: 100
SELECT *
FROM user_passwords
WHERE $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM user_passwords
WHERE user_id = @user_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO user_passwords
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-user_id,-created_at
UPDATE user_passwords
SET $fields
WHERE user_id = @user_id
RETURNING *;

-- @func: Delete
DELETE FROM user_passwords
WHERE user_id = @user_id
;

-- @func: SetVerified
UPDATE user_passwords
SET password_verified = true, updated_at = @updated_at
WHERE user_id = @user_id
;

