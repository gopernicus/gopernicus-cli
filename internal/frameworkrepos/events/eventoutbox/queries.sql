-- @database: primary

-- @func: List
-- @filter:conditions *
-- @search: ilike(event_type, correlation_id, tenant_id, aggregate_type, aggregate_id, status, worker_name, failure_reason)
-- @order: *
-- @max: 100
SELECT *
FROM event_outbox
WHERE $conditions AND $search
ORDER BY $order
LIMIT $limit
;

-- @func: Get
SELECT *
FROM event_outbox
WHERE event_id = @event_id
;

-- @func: Create
-- @fields: *,-created_at,-updated_at
INSERT INTO event_outbox
($fields)
VALUES ($values)
RETURNING *;

-- @func: Update
-- @fields: *,-event_id,-created_at
UPDATE event_outbox
SET $fields
WHERE event_id = @event_id
RETURNING *;

-- @func: Delete
DELETE FROM event_outbox
WHERE event_id = @event_id
;

