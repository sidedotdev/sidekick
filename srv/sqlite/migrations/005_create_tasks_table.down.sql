-- Drop tasks table
DROP TABLE IF EXISTS tasks;

-- Drop indexes
DROP INDEX IF EXISTS idx_tasks_workspace_id;
DROP INDEX IF EXISTS idx_tasks_id;
DROP INDEX IF EXISTS idx_tasks_status;
DROP INDEX IF EXISTS idx_tasks_archived;
DROP INDEX IF EXISTS idx_tasks_created;
DROP INDEX IF EXISTS idx_tasks_updated;