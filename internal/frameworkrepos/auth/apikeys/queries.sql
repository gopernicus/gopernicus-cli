-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(name, last_used_ip)
-- @order: *
-- @max: 100
SELECT *
FROM api_keys
WHERE parent_service_account_id = @parent_service_account_id AND $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM api_keys
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO api_keys
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-api_key_id,-parent_service_account_id,-record_state,-created_at
UPDATE api_keys
SET $fields
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
RETURNING *;

-- @func: SoftDelete
UPDATE api_keys
SET record_state = 'deleted'
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
;

-- @func: Archive
UPDATE api_keys
SET record_state = 'archived'
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
;

-- @func: Restore
UPDATE api_keys
SET record_state = 'active'
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
;

-- @func: Delete
DELETE FROM api_keys
WHERE api_key_id = @api_key_id AND parent_service_account_id = @parent_service_account_id
;

