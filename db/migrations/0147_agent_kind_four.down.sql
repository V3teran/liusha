ALTER TABLE agent DROP CONSTRAINT agent_kind_check;
ALTER TABLE agent ADD CONSTRAINT agent_kind_check
    CHECK (kind = ANY (ARRAY['planner'::text, 'executor'::text]));
