-- Drop the index created in the up migration
DROP INDEX IF EXISTS idx_workspaces_name;

-- Drop the workspaces table
DROP TABLE IF EXISTS workspaces;