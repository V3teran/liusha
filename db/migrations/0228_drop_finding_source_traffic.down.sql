ALTER TABLE public.finding ADD COLUMN source_traffic_id bigint;
CREATE INDEX finding_source_traffic_idx ON public.finding USING btree (source_traffic_id) WHERE (source_traffic_id IS NOT NULL);
