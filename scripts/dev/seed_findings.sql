-- dev 种子：为漏洞管理模块灌可视测试数据。
-- 依赖 seed_traffic.sql 先跑过（复用其建的 2 个 seed task + seed.* host 的 proxy_traffic 行）。
-- 覆盖：5 种 severity、5 种 triage 状态、跨 host、source_traffic_id 关联、depends_on 组合漏洞链。
-- 幂等：先按固定 host 清掉本脚本注入的行再重灌。
-- traffic id 不硬编码：seed_traffic.sql 每次重灌都产生新 proxy_traffic id（bigserial 不回退），
-- 这里按 host+method+path 动态查最新一条，避免关联到已被清理的旧 id。
BEGIN;

DELETE FROM finding WHERE host IN ('seed.shop.local', 'seed.api.local', 'seed.cdn.local');

WITH shop_task AS (SELECT id FROM task WHERE target_host = 'seed.shop.local' LIMIT 1),
     api_task  AS (SELECT id FROM task WHERE target_host = 'seed.api.local'  LIMIT 1),
     t_login   AS (SELECT id FROM proxy_traffic WHERE host = 'seed.shop.local' AND method = 'POST'   AND path = '/api/login'       ORDER BY id DESC LIMIT 1),
     t_delete  AS (SELECT id FROM proxy_traffic WHERE host = 'seed.shop.local' AND method = 'DELETE' AND path = '/api/products/10' ORDER BY id DESC LIMIT 1),
     t_putuser AS (SELECT id FROM proxy_traffic WHERE host = 'seed.api.local'  AND method = 'PUT'    AND path = '/v1/users/42'     ORDER BY id DESC LIMIT 1),
     t_appjs   AS (SELECT id FROM proxy_traffic WHERE host = 'seed.cdn.local'  AND method = 'GET'    AND path = '/assets/app.js'   ORDER BY id DESC LIMIT 1),

     -- 1) 弱会话令牌：login 返回的 token 无签名轮换，可预测重放（open，关联登录流量）。
     f_token AS (
       INSERT INTO finding (task_id, host, severity, summary, target, evidence,
                             cwe_id, owasp_category, remediation, source_traffic_id, status)
       SELECT shop_task.id, 'seed.shop.local', 'high',
              '弱会话令牌 - login 返回的 JWT 无签名轮换机制，可预测重放',
              '{"method":"POST","path":"/api/login"}'::jsonb,
              '{"observation":"多次登录同一账号返回结构相同的 JWT，签名密钥长期未轮换","repro_cmd":"curl -s -X POST https://seed.shop.local/api/login -d ''{\"username\":\"admin\",\"password\":\"s3cr3t\"}''"}'::jsonb,
              'CWE-330', 'A02:2021', '引入短期 token + refresh 机制，定期轮换签名密钥', t_login.id, 'open'
       FROM shop_task, t_login
       RETURNING id
     ),

     -- 2) 错误信息泄露：DELETE 500 直接把内部 trace_id 抛给客户端（confirmed，关联删除流量）。
     f_errdisclosure AS (
       INSERT INTO finding (task_id, host, severity, summary, target, evidence,
                             cwe_id, owasp_category, remediation, source_traffic_id, status,
                             triage_note, triaged_at)
       SELECT shop_task.id, 'seed.shop.local', 'medium',
              '服务端错误信息泄露 - 500 响应直接暴露内部 trace_id',
              '{"method":"DELETE","path":"/api/products/10"}'::jsonb,
              '{"observation":"删除商品触发未捕获异常，响应体带 trace_id 可反查内部日志系统","repro_cmd":"curl -s -X DELETE https://seed.shop.local/api/products/10 -H ''Authorization: Bearer <token>''"}'::jsonb,
              'CWE-209', 'A05:2021', '统一错误处理，生产环境响应体只返回通用错误码，trace_id 仅落服务端日志', t_delete.id, 'confirmed',
              '已复现，trace_id 与内部日志系统 ID 格式一致，确认信息泄露', now() - interval '2 hours'
       FROM shop_task, t_delete
       RETURNING id
     ),

     -- 3) 越权访问控制缺失：PUT /v1/users/42 未校验调用方是否为该用户本人（open，关联改资料流量）。
     f_idor AS (
       INSERT INTO finding (task_id, host, severity, summary, target, evidence,
                             cwe_id, owasp_category, remediation, source_traffic_id, status)
       SELECT api_task.id, 'seed.api.local', 'critical',
              '越权访问控制缺失 - 任意用户资料可被覆写(IDOR)',
              '{"method":"PUT","path":"/v1/users/42"}'::jsonb,
              '{"observation":"用非 42 号用户的合法 token 请求 PUT /v1/users/42 依然 200 成功修改邮箱","repro_cmd":"curl -s -X PUT https://seed.api.local/v1/users/42 -H ''Authorization: Bearer <other-user-token>'' -d ''{\"email\":\"attacker@evil.com\"}''"}'::jsonb,
              'CWE-639', 'A01:2021', '服务端校验 token 所属用户 ID 与路径参数一致，拒绝跨用户写操作', t_putuser.id, 'open'
       FROM api_task, t_putuser
       RETURNING id
     ),

     -- 4) 用户名枚举嫌疑：401 响应内容差异——复测后判定误报（false_positive）。
     f_enum AS (
       INSERT INTO finding (task_id, host, severity, summary, target, evidence,
                             cwe_id, owasp_category, remediation, source_traffic_id, status,
                             triage_note, triaged_at)
       SELECT api_task.id, 'seed.api.local', 'low',
              '认证提示信息可能可枚举有效用户名',
              '{"method":"GET","path":"/v1/users/42"}'::jsonb,
              '{"observation":"未带 token 请求返回 401 且提示 missing or invalid token，怀疑不同用户名响应有差异"}'::jsonb,
              'CWE-203', 'A07:2021', '统一鉴权失败提示文案，不区分用户是否存在', NULL, 'false_positive',
              '复测 10 个不同用户名路径，响应体/状态码完全一致，无法枚举，判定误报', now() - interval '1 hour'
       FROM api_task
       RETURNING id
     ),

     -- 5) 静态资源缓存策略过宽：cdn 的长缓存 + 无版本哈希，业务方评估后接受风险（accepted）。
     f_cache AS (
       INSERT INTO finding (task_id, host, severity, summary, target, evidence,
                             cwe_id, owasp_category, remediation, source_traffic_id, status,
                             triage_note, triaged_at)
       SELECT shop_task.id, 'seed.cdn.local', 'info',
              '静态资源缓存策略过于宽松 - max-age 一年且无内容哈希',
              '{"method":"GET","path":"/assets/app.js"}'::jsonb,
              '{"observation":"Cache-Control: public, max-age=31536000，文件名无 hash，发版后客户端可能长期用旧资源"}'::jsonb,
              'CWE-524', 'A05:2021', '静态资源改用内容哈希文件名 + 合理缓存策略', t_appjs.id, 'accepted',
              '业务方评估该风险可接受，CDN 发版流程已有强制刷新手段，暂不修复', now() - interval '30 minutes'
       FROM shop_task, t_appjs
       RETURNING id
     )

-- 6) 账户接管链：弱会话令牌(f_token) + 越权改资料(f_idor) 组合可完全接管任意账号（confirmed，depends_on 两个前置 finding）。
INSERT INTO finding (task_id, host, severity, summary, target, evidence,
                      cwe_id, owasp_category, remediation, status, depends_on,
                      triage_note, triaged_at)
SELECT shop_task.id, 'seed.shop.local', 'critical',
       '账户接管链 - 弱会话令牌 + 越权改资料组合可完全接管任意账号',
       '{}'::jsonb,
       '{"observation":"先用弱令牌漏洞获取任意有效 token，再用 IDOR 漏洞改写目标用户邮箱，即可发起密码重置接管账号","repro_cmd":"参见依赖漏洞各自 repro_cmd，链式执行"}'::jsonb,
       NULL, 'A01:2021', '两个前置漏洞任一修复即可阻断本链；建议优先修复 IDOR', 'confirmed',
       ARRAY[(SELECT id FROM f_token), (SELECT id FROM f_idor)],
       '复盘确认两个独立漏洞可组合成完整攻击链，优先级提升为 critical', now() - interval '10 minutes'
FROM shop_task, f_token, f_idor;

COMMIT;

-- 7) 批量灌 80 条到独立 host（seed.bulk.local），仅用于验证分页/每页条数选择器——
-- 与上面 6 条精心构造的语义化种子分开。挂到 api_task（不依赖具体 traffic 行）。
BEGIN;

DELETE FROM finding WHERE host = 'seed.bulk.local';

INSERT INTO finding (task_id, host, severity, summary, target, evidence, status)
SELECT
  api_task.id,
  'seed.bulk.local',
  (ARRAY['critical','high','medium','low','info'])[1 + (n % 5)],
  '批量测试漏洞 #' || n || ' - 用于验证分页',
  jsonb_build_object('method', 'GET', 'path', '/api/bulk/' || n),
  jsonb_build_object('observation', '批量种子占位证据 ' || n),
  (ARRAY['open','confirmed','fixed','false_positive','accepted'])[1 + (n % 5)]
FROM (SELECT id FROM task WHERE target_host = 'seed.api.local' LIMIT 1) AS api_task,
     generate_series(1, 80) AS n;

COMMIT;

-- 摘要
SELECT f.seq, f.host, f.severity, f.status, f.summary, array_length(f.depends_on, 1) AS deps
FROM finding f
WHERE f.host LIKE 'seed.%'
ORDER BY f.seq DESC
LIMIT 20;

SELECT count(*) AS total_seed_findings FROM finding WHERE host LIKE 'seed.%';
