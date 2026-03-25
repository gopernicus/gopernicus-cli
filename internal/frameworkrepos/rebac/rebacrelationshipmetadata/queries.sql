-- @database: primary

-- @func: List
-- @filter:conditions *
-- @order: *
-- @max: 100
SELECT *
FROM rebac_relationship_metadata
WHERE $conditions
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM rebac_relationship_metadata
WHERE relationship_id = @relationship_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO rebac_relationship_metadata
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-relationship_id,-created_at
UPDATE rebac_relationship_metadata
SET $fields
WHERE relationship_id = @relationship_id
RETURNING *;

-- @func: Delete
DELETE FROM rebac_relationship_metadata
WHERE relationship_id = @relationship_id
;

