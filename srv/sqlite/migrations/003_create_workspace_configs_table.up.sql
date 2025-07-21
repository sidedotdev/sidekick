-- Create workspace_config table
CREATE TABLE IF NOT EXISTS workspace_configs (
    workspace_id TEXT PRIMARY KEY,
    llm_config TEXT NOT NULL,
    embedding_config TEXT NOT NULL,
    FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);