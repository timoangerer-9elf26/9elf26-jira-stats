-- +goose Up
-- meta is a small key/value table for sync bookkeeping (e.g. the last_sync
-- timestamp the incremental sync bounds its query on). Part of the rebuildable
-- projection: dropping it just costs a fresh full backfill.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE meta;
