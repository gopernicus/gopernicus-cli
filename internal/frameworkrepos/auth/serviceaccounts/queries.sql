-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(name, description)
-- @order: *
-- @max: 100
SELECT *
FROM service_accounts
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM service_accounts
WHERE service_account_id = @service_account_id
;

-- Create is handled by a custom store method (transactional principal + service account insert).
-- See serviceaccountspgx/store.go.

-- @func: Update
-- @fields: *,-service_account_id,-record_state,-created_at
UPDATE service_accounts
SET $fields
WHERE service_account_id = @service_account_id
RETURNING *;

-- @func: SoftDelete
UPDATE service_accounts
SET record_state = 'deleted'
WHERE service_account_id = @service_account_id
;

-- @func: Archive
UPDATE service_accounts
SET record_state = 'archived'
WHERE service_account_id = @service_account_id
;

-- @func: Restore
UPDATE service_accounts
SET record_state = 'active'
WHERE service_account_id = @service_account_id
;

-- @func: Delete
DELETE FROM service_accounts
WHERE service_account_id = @service_account_id
;

