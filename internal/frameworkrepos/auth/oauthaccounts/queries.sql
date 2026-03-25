-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(provider, provider_user_id, provider_email, scope)
-- @order: *
-- @max: 100
SELECT *
FROM oauth_accounts
WHERE parent_user_id = @parent_user_id AND $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM oauth_accounts
WHERE oauth_account_id = @oauth_account_id AND parent_user_id = @parent_user_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO oauth_accounts
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-oauth_account_id,-parent_user_id,-created_at
UPDATE oauth_accounts
SET $fields
WHERE oauth_account_id = @oauth_account_id AND parent_user_id = @parent_user_id
RETURNING *;

-- @func: Delete
DELETE FROM oauth_accounts
WHERE oauth_account_id = @oauth_account_id AND parent_user_id = @parent_user_id
;

