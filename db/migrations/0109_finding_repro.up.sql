-- 0109: finding 加 repro jsonb —— 机器可验的复现配方（喂 L4 Verifier 复现门）。
--
-- 动机：evidence 是人读证据（req/resp 摘录、PoC 叙述），机器无法据此自动复现坐实。
--   L4 认知环要把 finding 晋升成世界模型 KindFinding 节点，必须过 Verifier 复现门，
--   而复现门吃的是**结构化配方**（web 域 = {traffic_id, modifications, assert}）：
--   拿哪条源流量、怎么改写、拿什么断言判坐实。这套配方与人读 evidence 语义正交，
--   故立独立列而非塞进 evidence。
--
--   repro 形状 domain-specific（web/binary/cloud 各异），DB 层只存 jsonb 不加 CHECK；
--   语义校验归各域 Replayer（web.Replayer 解析 ReplayRecipe，空断言拒绝坐实）。
--   可空：历史 finding 及 LM 未产配方时为 NULL（此时无法自动晋升，仅留人读记录）。

ALTER TABLE finding ADD COLUMN repro jsonb;

COMMENT ON COLUMN finding.repro IS '机器可验的复现配方（web={traffic_id,modifications,assert}）；喂 L4 Verifier 复现门自动晋升成世界模型节点。NULL=未产配方（仅人读记录，不自动晋升）';
