ALTER TABLE insight ALTER COLUMN id TYPE text USING id::text;
ALTER TABLE insight ALTER COLUMN assignment_id TYPE text USING assignment_id::text;
ALTER TABLE insight ALTER COLUMN source_task_id TYPE text USING source_task_id::text;
ALTER TABLE insight ALTER COLUMN source_agent_run_id TYPE text USING source_agent_run_id::text;
