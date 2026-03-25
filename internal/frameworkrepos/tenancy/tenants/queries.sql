-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(name, slug, description)
-- @order: *
-- @max: 100
SELECT *
FROM tenants
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM tenants
WHERE tenant_id = @tenant_id
;

-- @func: GetBySlug
SELECT *
FROM tenants
WHERE slug = @slug
AND record_state = 'active'
;

-- @func: GetIDBySlug
-- @returns: tenant_id
SELECT tenant_id
FROM tenants
WHERE slug = @slug
AND record_state = 'active'
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO tenants
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-tenant_id,-record_state,-created_at
UPDATE tenants
SET $fields
WHERE tenant_id = @tenant_id
RETURNING *;

-- @func: SoftDelete
UPDATE tenants
SET record_state = 'deleted'
WHERE tenant_id = @tenant_id
;

-- @func: Archive
UPDATE tenants
SET record_state = 'archived'
WHERE tenant_id = @tenant_id
;

-- @func: Restore
UPDATE tenants
SET record_state = 'active'
WHERE tenant_id = @tenant_id
;

-- @func: Delete
DELETE FROM tenants
WHERE tenant_id = @tenant_id
;

