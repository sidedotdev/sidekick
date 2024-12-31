CREATE TABLE IF NOT EXISTS worktrees (
    id TEXT PRIMARY KEY,
    flow_id TEXT NOT NULL,
    name TEXT NOT NULL,
    created DATETIME NOT NULL,
    workspace_id TEXT NOT NULL
);