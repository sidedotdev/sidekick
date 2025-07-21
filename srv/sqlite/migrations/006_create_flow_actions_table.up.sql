-- Create flow_actions table
CREATE TABLE IF NOT EXISTS flow_actions (
    id TEXT PRIMARY KEY,
    subflow_name TEXT NOT NULL,
    subflow_description TEXT NOT NULL,
    subflow_id TEXT,
    flow_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    created DATETIME NOT NULL,
    updated DATETIME NOT NULL,
    action_type TEXT NOT NULL,
    action_params TEXT NOT NULL,  -- Store as JSON string
    action_status TEXT NOT NULL,
    action_result TEXT,
    is_human_action BOOLEAN NOT NULL,
    is_callback_action BOOLEAN NOT NULL
);

-- Create indexes for faster lookups
CREATE INDEX IF NOT EXISTS idx_flow_actions_flow_id ON flow_actions(flow_id);
CREATE INDEX IF NOT EXISTS idx_flow_actions_workspace_id ON flow_actions(workspace_id);
CREATE INDEX IF NOT EXISTS idx_flow_actions_subflow_id ON flow_actions(subflow_id);