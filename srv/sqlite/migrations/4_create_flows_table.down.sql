-- Drop indexes
DROP INDEX IF EXISTS idx_flows_parent_id;
DROP INDEX IF EXISTS idx_flows_id;
DROP INDEX IF EXISTS idx_flows_workspace_id;

-- Drop flows table
DROP TABLE IF EXISTS flows;