ALTER TABLE tasks
    DROP CONSTRAINT IF EXISTS chk_tasks_target_profile_nonempty;

ALTER TABLE tasks
    DROP COLUMN IF EXISTS target_profile;
