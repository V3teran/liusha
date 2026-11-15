CREATE TABLE public.task_control_event (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    task_id uuid NOT NULL,
    command text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    processed_at timestamp with time zone,
    status text DEFAULT 'pending'::text NOT NULL,
    error text,
    CONSTRAINT task_control_event_pkey PRIMARY KEY (id),
    CONSTRAINT task_control_event_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'processed'::text, 'failed'::text])))
);
CREATE INDEX idx_task_control_event_created_at ON public.task_control_event USING btree (created_at);
CREATE INDEX idx_task_control_event_status ON public.task_control_event USING btree (status);
CREATE INDEX idx_task_control_event_task_id ON public.task_control_event USING btree (task_id);
