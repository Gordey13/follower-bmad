ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS source_task_id UUID NULL REFERENCES tasks(id) ON DELETE RESTRICT;

ALTER TABLE tasks
    DROP CONSTRAINT IF EXISTS chk_tasks_source_task_not_self;

ALTER TABLE tasks
    ADD CONSTRAINT chk_tasks_source_task_not_self CHECK (
        source_task_id IS NULL OR source_task_id <> id
    );

CREATE INDEX IF NOT EXISTS idx_tasks_source_task_id_created_at
    ON tasks (source_task_id, created_at DESC)
    WHERE source_task_id IS NOT NULL;
