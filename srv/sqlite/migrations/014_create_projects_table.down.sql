-- Drop the index created in the up migration
DROP INDEX IF EXISTS idx_projects_workspace_id;

-- Drop the projects table
DROP TABLE IF EXISTS projects;