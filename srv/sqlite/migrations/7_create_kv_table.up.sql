-- Create kv table
CREATE TABLE IF NOT EXISTS kv (
    workspace_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value BLOB,
    PRIMARY KEY (workspace_id, key)
);

-- Create index on key for faster lookups
CREATE INDEX IF NOT EXISTS idx_kv_key ON kv(workspace_id, key);