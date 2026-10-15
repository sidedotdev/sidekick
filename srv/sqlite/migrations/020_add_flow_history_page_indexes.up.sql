CREATE INDEX IF NOT EXISTS idx_flow_actions_workspace_flow_created_id
    ON flow_actions(workspace_id, flow_id, created, id);

CREATE INDEX IF NOT EXISTS idx_subflows_workspace_flow_id
    ON subflows(workspace_id, flow_id, id);