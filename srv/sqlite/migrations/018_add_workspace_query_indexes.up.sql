CREATE INDEX IF NOT EXISTS idx_flow_actions_workspace_flow
    ON flow_actions(workspace_id, flow_id);

CREATE INDEX IF NOT EXISTS idx_worktrees_workspace_flow
    ON worktrees(workspace_id, flow_id);

CREATE INDEX IF NOT EXISTS idx_flows_workspace_parent_created
    ON flows(workspace_id, parent_id, created, id);

CREATE INDEX IF NOT EXISTS idx_tasks_active_workspace_status
    ON tasks(workspace_id, status)
    WHERE archived IS NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_archived_workspace_order
    ON tasks(workspace_id, archived DESC, updated DESC)
    WHERE archived IS NOT NULL;