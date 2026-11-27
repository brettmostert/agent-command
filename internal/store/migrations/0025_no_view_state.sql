-- The view is each client's own (PAN-192): the daemon keeps no view state,
-- and no group's collapse.
DROP TABLE IF EXISTS view_state;
ALTER TABLE groups DROP COLUMN collapsed;
