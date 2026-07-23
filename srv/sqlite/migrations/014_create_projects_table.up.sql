-- Create projects table
CREATE TABLE IF NOT EXISTS projects (
    workspace_id TEXT NOT NULL,
    id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    priority TEXT NOT NULL,
    rank TEXT,
    created DATETIME NOT NULL,
    updated DATETIME NOT NULL,
    PRIMARY KEY (workspace_id, id)
);

-- Create index for faster lookups
CREATE INDEX IF NOT EXISTS idx_projects_workspace_id ON projects(workspace_id);