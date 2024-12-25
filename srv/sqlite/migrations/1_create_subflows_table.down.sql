-- Drop the index created in the up migration
DROP INDEX IF EXISTS idx_subflows_workspace_flow;

-- Drop the subflows table
DROP TABLE IF EXISTS subflows;