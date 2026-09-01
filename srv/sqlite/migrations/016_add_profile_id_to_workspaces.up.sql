-- Add optional profile association to workspaces, NULL meaning the default profile
ALTER TABLE workspaces ADD COLUMN profile_id TEXT;