ALTER TABLE public.exploration_node DROP CONSTRAINT ck_exploration_node_verification_status;

UPDATE public.exploration_node
   SET metadata = jsonb_set(metadata, '{refute_reason}', '"adjudication_failed"')
 WHERE kind = 'observation'
   AND metadata->>'refute_reason' = 'judge_unavailable';

UPDATE public.exploration_verification
   SET evidence = jsonb_set(evidence, '{refute_reason}', '"adjudication_failed"')
 WHERE evidence->>'refute_reason' = 'judge_unavailable';

UPDATE public.exploration_node
   SET metadata = (metadata - 'verification_status')
       || jsonb_build_object('confidence',
            CASE metadata->>'verification_status'
              WHEN 'pending' THEN 'unverified'
              WHEN 'confirmed' THEN 'verified'
              ELSE 'refuted'
            END)
       || CASE metadata->>'verification_status'
            WHEN 'pending' THEN '{}'::jsonb
            ELSE jsonb_build_object('verification_outcome', metadata->>'verification_status')
          END
 WHERE kind = 'observation'
   AND metadata ? 'verification_status';

UPDATE public.exploration_node
   SET metadata = metadata || '{"confidence":"factual"}'::jsonb
 WHERE kind = 'observation'
   AND NOT metadata ? 'confidence';

UPDATE public.exploration_node
   SET metadata = metadata || '{"confidence":"verified"}'::jsonb
 WHERE kind = 'result';

COMMENT ON COLUMN public.exploration_node.metadata IS 'GraphStore 元数据字段，存储业务特定属性';
