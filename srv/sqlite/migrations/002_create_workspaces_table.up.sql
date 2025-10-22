-- Create workspace table
CREATE TABLE IF NOT EXISTS workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    local_repo_dir TEXT NOT NULL,
    created DATETIME NOT NULL,
    updated DATETIME NOT NULL
);

-- Create index on name for faster lookups
CREATE INDEX IF NOT EXISTS idx_workspaces_name ON workspaces(name);