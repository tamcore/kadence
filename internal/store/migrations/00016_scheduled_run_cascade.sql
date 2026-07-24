-- +goose Up
ALTER TABLE scheduled_task_runs
    DROP CONSTRAINT scheduled_task_runs_task_id_fkey,
    ADD CONSTRAINT scheduled_task_runs_task_id_fkey
        FOREIGN KEY (task_id) REFERENCES scheduled_tasks(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE scheduled_task_runs
    DROP CONSTRAINT scheduled_task_runs_task_id_fkey,
    ADD CONSTRAINT scheduled_task_runs_task_id_fkey
        FOREIGN KEY (task_id) REFERENCES scheduled_tasks(id) ON DELETE RESTRICT;
