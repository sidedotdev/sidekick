CREATE INDEX IF NOT EXISTS idx_flow_actions_workspace_flow
    ON flow_actions(workspace_id, flow_id);

CREATE INDEX IF NOT EXISTS idx_worktrees_workspace_flow
    ON worktrees(workspace_id, flow_id);

CREATE INDEX IF NOT EXISTS idx_tasks_archived_workspace_order
    ON tasks(workspace_id, archived DESC, updated DESC)
    WHERE archived IS NOT NULL;