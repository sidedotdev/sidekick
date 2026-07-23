-- Add optional project assignment to tasks
ALTER TABLE tasks ADD COLUMN project_id TEXT NOT NULL DEFAULT '';