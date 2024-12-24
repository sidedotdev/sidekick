-- Drop the index created in the up migration
DROP INDEX IF EXISTS idx_kv_key;

-- Drop the kv table
DROP TABLE IF EXISTS kv;