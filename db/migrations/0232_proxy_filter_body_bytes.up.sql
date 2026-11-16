-- 0232：代理过滤的两个 body 上限字段改名，单位后缀统一为 _bytes（与配置键
-- proxy.max_request_body_bytes / max_response_body_bytes 同名）。改写已保存的用户自定义。

UPDATE public.setting_override
   SET fields = (fields - 'max_request_body_size') || jsonb_build_object('max_request_body_bytes', fields->'max_request_body_size')
 WHERE group_key = 'proxy_filter' AND fields ? 'max_request_body_size';

UPDATE public.setting_override
   SET fields = (fields - 'max_response_body_size') || jsonb_build_object('max_response_body_bytes', fields->'max_response_body_size')
 WHERE group_key = 'proxy_filter' AND fields ? 'max_response_body_size';

COMMENT ON TABLE public.setting_override IS '系统设置的用户自定义层：只存被自定义的字段；出厂默认 = 代码 + 配置文件部署值';
