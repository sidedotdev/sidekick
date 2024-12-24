-- Create flows table
CREATE TABLE IF NOT EXISTS flows (
    id TEXT NOT NULL PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    type TEXT NOT NULL,
    parent_id TEXT NOT NULL,
    status TEXT NOT NULL,
    created DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for faster lookups
CREATE INDEX IF NOT EXISTS idx_flows_workspace_id ON flows(workspace_id);
CREATE INDEX IF NOT EXISTS idx_flows_parent_id ON flows(parent_id);