-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(name, slug, description)
-- @order: *
-- @max: 100
SELECT *
FROM groups
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM groups
WHERE group_id = @group_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO groups
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-group_id,-record_state,-created_at
UPDATE groups
SET $fields
WHERE group_id = @group_id
RETURNING *;

-- @func: SoftDelete
UPDATE groups
SET record_state = 'deleted'
WHERE group_id = @group_id
;

-- @func: Archive
UPDATE groups
SET record_state = 'archived'
WHERE group_id = @group_id
;

-- @func: Restore
UPDATE groups
SET record_state = 'active'
WHERE group_id = @group_id
;

-- @func: Delete
DELETE FROM groups
WHERE group_id = @group_id
;

