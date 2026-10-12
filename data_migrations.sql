-- name: tory.lock-data-migrations
SELECT pg_advisory_lock(hashtext('tory.data_migrations'));

-- name: tory.create-table-data-migrations
CREATE TABLE IF NOT EXISTS data_migrations (
    name TEXT NOT NULL PRIMARY KEY,
    applied_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- name: tory.list-data-migrations
SELECT
    name,
    applied_at
FROM data_migrations
ORDER BY applied_at, name;

-- name: tory.record-data-migration
INSERT INTO data_migrations (name)
VALUES (:name)
RETURNING
    name,
    applied_at;
