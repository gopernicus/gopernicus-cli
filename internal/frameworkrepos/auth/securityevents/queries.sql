-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(event_type, event_status, ip_address, user_agent)
-- @order: *
-- @max: 100
SELECT *
FROM security_events
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM security_events
WHERE event_id = @event_id
;

-- @func: Create
-- @fields: *,-created_at
INSERT INTO security_events
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-event_id,-created_at
UPDATE security_events
SET $fields
WHERE event_id = @event_id
RETURNING *;

-- @func: Delete
DELETE FROM security_events
WHERE event_id = @event_id
;

