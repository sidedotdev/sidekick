-- Create kv table
CREATE TABLE IF NOT EXISTS kv (
    workspace_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value BLOB,
    PRIMARY KEY (workspace_id, key)
);