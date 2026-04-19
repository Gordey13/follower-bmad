DROP INDEX IF EXISTS idx_tasks_source_task_id_created_at;

ALTER TABLE tasks
    DROP CONSTRAINT IF EXISTS chk_tasks_source_task_not_self;

ALTER TABLE tasks
    DROP COLUMN IF EXISTS source_task_id;
