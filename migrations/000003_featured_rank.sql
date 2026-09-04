-- Editors order the featured shelf by hand. A NULL rank means "unranked", and
-- unranked apps fall back to most-recently-updated so the shelf still fills in
-- without anyone assigning numbers.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS featured_rank integer;
CREATE INDEX IF NOT EXISTS apps_featured_rank_idx
    ON apps (featured_rank NULLS LAST, updated_at DESC) WHERE featured;
