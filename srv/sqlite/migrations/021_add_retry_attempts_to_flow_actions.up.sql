-- Track failed attempts and the scheduled automatic retry of actions being retried
ALTER TABLE flow_actions ADD COLUMN attempts TEXT;
ALTER TABLE flow_actions ADD COLUMN next_retry_at TIMESTAMP;