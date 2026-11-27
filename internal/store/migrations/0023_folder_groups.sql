-- A group with a path is a folder: new sessions and schedules in it start
-- there. '' is a group, which has no path.
ALTER TABLE groups ADD COLUMN path TEXT NOT NULL DEFAULT '';
