-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(principal_type)
-- @order: *
-- @max: 100
SELECT *
FROM principals
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM principals
WHERE principal_id = @principal_id
;

-- @func: Create
-- @fields: *,-created_at
INSERT INTO principals
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-principal_id,-created_at
UPDATE principals
SET $fields
WHERE principal_id = @principal_id
RETURNING *;

-- @func: Delete
DELETE FROM principals
WHERE principal_id = @principal_id
;

