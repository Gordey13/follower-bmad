ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS target_profile TEXT;

UPDATE tasks
SET target_profile = 'legacy:' || id::TEXT
WHERE target_profile IS NULL
   OR BTRIM(target_profile) = '';

ALTER TABLE tasks
    ALTER COLUMN target_profile SET NOT NULL;

ALTER TABLE tasks
    DROP CONSTRAINT IF EXISTS chk_tasks_target_profile_nonempty;

ALTER TABLE tasks
    ADD CONSTRAINT chk_tasks_target_profile_nonempty CHECK (
        NULLIF(BTRIM(target_profile), '') IS NOT NULL
    );
