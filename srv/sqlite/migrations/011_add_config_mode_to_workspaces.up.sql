-- Add config_mode column to workspaces table
ALTER TABLE workspaces ADD COLUMN config_mode TEXT NOT NULL DEFAULT 'merge';

-- Set all existing workspaces to 'merge' mode to maintain current behavior
UPDATE workspaces SET config_mode = 'merge' WHERE config_mode IS NULL OR config_mode = '';