-- Drop flow_actions table
DROP TABLE IF EXISTS flow_actions;

-- Drop indexes
DROP INDEX IF EXISTS idx_flow_actions_flow_id;
DROP INDEX IF EXISTS idx_flow_actions_workspace_id;
DROP INDEX IF EXISTS idx_flow_actions_subflow_id;