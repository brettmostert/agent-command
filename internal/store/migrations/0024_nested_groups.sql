-- A group can be inside another (two levels): parent_id is its parent's id,
-- '' at the top. The daemon checks the rules (no third level, no cycles).
ALTER TABLE groups ADD COLUMN parent_id TEXT NOT NULL DEFAULT '';
