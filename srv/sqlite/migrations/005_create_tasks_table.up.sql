-- Create tasks table
CREATE TABLE IF NOT EXISTS tasks (
    workspace_id TEXT NOT NULL,
    id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    status TEXT NOT NULL,
    links TEXT,  -- Stored as JSON string
    agent_type TEXT NOT NULL,
    flow_type TEXT NOT NULL,
    archived DATETIME,
    created DATETIME NOT NULL,
    updated DATETIME NOT NULL,
    flow_options TEXT,  -- Stored as JSON string
    PRIMARY KEY (workspace_id, id)
);

-- Create indexes for faster lookups
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_id ON tasks(workspace_id);
CREATE INDEX IF NOT EXISTS idx_tasks_id ON tasks(id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_archived ON tasks(archived);
CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks(created);
CREATE INDEX IF NOT EXISTS idx_tasks_updated ON tasks(updated);