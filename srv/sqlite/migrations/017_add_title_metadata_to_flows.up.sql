-- Add human-readable title and generic runtime metadata to flows
ALTER TABLE flows ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE flows ADD COLUMN metadata TEXT;