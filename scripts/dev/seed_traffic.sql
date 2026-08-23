-- dev 种子：为流量模块（Burp 式详情）灌可视测试数据。
-- 覆盖：多 host / 多 method / 2xx·4xx·5xx / JSON 与 HTML body / 已消费(M:N)与未消费。
-- raw 报文用 convert_to(text,'UTF8') 转 bytea，与 ingest 侧组装的字节语义一致。
-- 幂等：assignment/task/conversation 用固定 UUID（不是每次重灌都新建一套，避免历史脚本
-- 反复运行攒出多组重复 seed task——曾踩坑：无条件 INSERT 导致 4 组 seed task 混杂在真实数据里）。
BEGIN;

-- 清理上一次种子（仅本脚本的 seed host），避免重复累积。
DELETE FROM proxy_traffic WHERE host IN ('seed.shop.local', 'seed.api.local', 'seed.cdn.local');

-- 1) assignment + 2 个 task（passive 消费方）+ 绑定会话（chip 跳转目标）。固定 UUID（5eed 前缀，
-- 十六进制合法字符 5/e/e/d 拼读近似 "seed"），ON CONFLICT 跳过重建，避免重灌攒出多组重复行。
INSERT INTO assignment (id, source, scenario_id, title, payload)
VALUES ('5eed0000-0000-0000-0000-000000000001'::uuid, 'auto', 'api-pentest', 'seed 流量演示', '[]'::jsonb)
ON CONFLICT (id) DO NOTHING;

INSERT INTO task (id, assignment_id, scenario_id, target_host, status, brief)
VALUES
  ('5eed0000-0000-0000-0000-000000000002'::uuid, '5eed0000-0000-0000-0000-000000000001'::uuid, 'api-pentest', 'seed.shop.local', 'active', 'seed'),
  ('5eed0000-0000-0000-0000-000000000003'::uuid, '5eed0000-0000-0000-0000-000000000001'::uuid, 'api-pentest', 'seed.api.local', 'completed', 'seed')
ON CONFLICT (id) DO NOTHING;

INSERT INTO conversation (id, title, task_id)
VALUES
  ('5eed0000-0000-0000-0000-000000000004'::uuid, 'seed 会话 · seed.shop.local', '5eed0000-0000-0000-0000-000000000002'::uuid),
  ('5eed0000-0000-0000-0000-000000000005'::uuid, 'seed 会话 · seed.api.local', '5eed0000-0000-0000-0000-000000000003'::uuid)
ON CONFLICT (id) DO NOTHING;

COMMIT;

-- 2) proxy_traffic：整条 raw 报文（请求行/头/体一体）。
BEGIN;

-- resp_len = 响应体字节数（不含响应头），与 ingest 侧 len(snap.ResponseBody) 语义一致，
-- 逐条按 body 实际 UTF-8 字节数手填（避免种子数据和真实抓包出现"长度列全 0"的落差）。
INSERT INTO proxy_traffic (host, method, scheme, url, path, status_code, resp_len, content_type, http_version, request_raw, response_raw)
VALUES
  ('seed.shop.local', 'POST', 'https', 'https://seed.shop.local/api/login', '/api/login', 200, 85, 'application/json', 'HTTP/1.1',
   convert_to(E'POST /api/login HTTP/1.1\r\nHost: seed.shop.local\r\nContent-Type: application/json\r\nAccept: application/json\r\nUser-Agent: liusha-proxy\r\n\r\n{"username":"admin","password":"s3cr3t"}', 'UTF8'),
   convert_to(E'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nSet-Cookie: session=abc123; HttpOnly\r\n\r\n{"ok":true,"token":"eyJhbGciOiJIUzI1NiJ9.payload.sig","user":{"id":1,"role":"admin"}}', 'UTF8')),

  ('seed.shop.local', 'GET', 'https', 'https://seed.shop.local/api/products?limit=2', '/api/products', 200, 99, 'application/json', 'HTTP/1.1',
   convert_to(E'GET /api/products?limit=2 HTTP/1.1\r\nHost: seed.shop.local\r\nAuthorization: Bearer eyJhbGciOiJIUzI1NiJ9\r\nAccept: application/json\r\n\r\n', 'UTF8'),
   convert_to(E'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"items":[{"id":10,"name":"Widget","price":9.99},{"id":11,"name":"Gadget","price":19.5}],"total":2}', 'UTF8')),

  ('seed.shop.local', 'DELETE', 'https', 'https://seed.shop.local/api/products/10', '/api/products/10', 500, 48, 'application/json', 'HTTP/1.1',
   convert_to(E'DELETE /api/products/10 HTTP/1.1\r\nHost: seed.shop.local\r\nAuthorization: Bearer eyJhbGciOiJIUzI1NiJ9\r\n\r\n', 'UTF8'),
   convert_to(E'HTTP/1.1 500 Internal Server Error\r\nContent-Type: application/json\r\n\r\n{"error":"unexpected error","trace_id":"9f3c2a"}', 'UTF8')),

  ('seed.api.local', 'GET', 'https', 'https://seed.api.local/v1/users/42', '/v1/users/42', 401, 36, 'application/json', 'HTTP/1.1',
   convert_to(E'GET /v1/users/42 HTTP/1.1\r\nHost: seed.api.local\r\nAccept: application/json\r\n\r\n', 'UTF8'),
   convert_to(E'HTTP/1.1 401 Unauthorized\r\nContent-Type: application/json\r\nWWW-Authenticate: Bearer\r\n\r\n{"error":"missing or invalid token"}', 'UTF8')),

  ('seed.api.local', 'PUT', 'https', 'https://seed.api.local/v1/users/42', '/v1/users/42', 200, 73, 'application/json', 'HTTP/1.1',
   convert_to(E'PUT /v1/users/42 HTTP/1.1\r\nHost: seed.api.local\r\nContent-Type: application/json\r\nAuthorization: Bearer valid-token\r\n\r\n{"email":"new@example.com","display_name":"Alice"}', 'UTF8'),
   convert_to(E'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"id":42,"email":"new@example.com","display_name":"Alice","updated":true}', 'UTF8')),

  ('seed.cdn.local', 'GET', 'https', 'https://seed.cdn.local/assets/app.js', '/assets/app.js', 200, 31, 'application/javascript', 'HTTP/2.0',
   convert_to(E'GET /assets/app.js HTTP/2.0\r\nHost: seed.cdn.local\r\nAccept: */*\r\n\r\n', 'UTF8'),
   convert_to(E'HTTP/2.0 200 OK\r\nContent-Type: application/javascript\r\nCache-Control: public, max-age=31536000\r\n\r\nconsole.log("hello from seed");', 'UTF8')),

  ('seed.cdn.local', 'GET', 'https', 'https://seed.cdn.local/index.html', '/index.html', 404, 53, 'text/html', 'HTTP/2.0',
   convert_to(E'GET /index.html HTTP/2.0\r\nHost: seed.cdn.local\r\nAccept: text/html\r\n\r\n', 'UTF8'),
   convert_to(E'HTTP/2.0 404 Not Found\r\nContent-Type: text/html\r\n\r\n<!doctype html><html><body><h1>404</h1></body></html>', 'UTF8'));

COMMIT;

-- 2b) 批量灌 120 条到独立 host（seed.bulk.local），仅用于验证分页/每页条数选择器——
-- 与上面 7 条精心构造的语义化种子分开，避免筛选/详情演示数据被淹没。未消费（不挂 traffic_task）。
BEGIN;

DELETE FROM proxy_traffic WHERE host = 'seed.bulk.local';

INSERT INTO proxy_traffic (host, method, scheme, url, path, status_code, resp_len, content_type, http_version, request_raw, response_raw)
SELECT
  'seed.bulk.local',
  (ARRAY['GET','POST','PUT','DELETE'])[1 + (n % 4)],
  'https',
  'https://seed.bulk.local/api/items/' || n,
  '/api/items/' || n,
  (ARRAY[200,201,400,404,500])[1 + (n % 5)],
  40 + (n % 200),
  'application/json',
  'HTTP/1.1',
  convert_to(E'GET /api/items/' || n || E' HTTP/1.1\r\nHost: seed.bulk.local\r\n\r\n', 'UTF8'),
  convert_to(E'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{"id":' || n || E'}', 'UTF8')
FROM generate_series(1, 120) AS n;

COMMIT;

-- 3) M:N 关联：shop 的 3 条 → active task；api 的 2 条 → completed task；
--    其中登录一条同时被两个 task 消费（演示多对多）。cdn 两条保持未消费。
BEGIN;

WITH shop_task AS (SELECT id FROM task WHERE target_host = 'seed.shop.local' LIMIT 1),
     api_task  AS (SELECT id FROM task WHERE target_host = 'seed.api.local'  LIMIT 1)
INSERT INTO traffic_task (traffic_id, task_id)
SELECT p.id, st.id FROM proxy_traffic p, shop_task st WHERE p.host = 'seed.shop.local'
UNION ALL
SELECT p.id, at.id FROM proxy_traffic p, api_task at WHERE p.host = 'seed.api.local'
UNION ALL
-- 登录条额外挂到 api_task，形成 M:N（一条流量两个消费者）。
SELECT p.id, at.id FROM proxy_traffic p, api_task at WHERE p.host = 'seed.shop.local' AND p.path = '/api/login'
ON CONFLICT DO NOTHING;

COMMIT;

-- 摘要
SELECT p.id, p.host, p.method, p.status_code, p.content_type, count(tt.task_id) AS consumers
FROM proxy_traffic p
LEFT JOIN traffic_task tt ON tt.traffic_id = p.id
WHERE p.host LIKE 'seed.%'
GROUP BY p.id, p.host, p.method, p.status_code, p.content_type
ORDER BY p.id;
