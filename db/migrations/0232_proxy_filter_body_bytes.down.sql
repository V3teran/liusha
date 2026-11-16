UPDATE public.setting_override
   SET fields = (fields - 'max_request_body_bytes') || jsonb_build_object('max_request_body_size', fields->'max_request_body_bytes')
 WHERE group_key = 'proxy_filter' AND fields ? 'max_request_body_bytes';

UPDATE public.setting_override
   SET fields = (fields - 'max_response_body_bytes') || jsonb_build_object('max_response_body_size', fields->'max_response_body_bytes')
 WHERE group_key = 'proxy_filter' AND fields ? 'max_response_body_bytes';

COMMENT ON TABLE public.setting_override IS '系统设置的用户自定义层：只存被自定义的字段；出厂默认 = 代码 + config.yaml 部署值';
