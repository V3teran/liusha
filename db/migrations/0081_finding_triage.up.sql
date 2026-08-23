-- 0081: finding 加 triage 处置字段（漏洞页从「只读片段」升级为「全局台账 + 处置」）。
--
-- 背景：漏洞页原走 /sitemap（仅 active），2/3 的 passive 漏洞不可见。改为全局台账后
-- 需要处置生命周期——对齐 DefectDojo / GitHub Security 的 triage 状态机（业界通用五态）。
--
-- status 五态：
--   open           待处理（新漏洞默认，write_finding 落库即此态，LLM 侧无感）
--   confirmed      已确认（人工核实为真实漏洞，待修）
--   fixed          已修复
--   false_positive 误报（LLM 挖错 / 非真实漏洞）
--   accepted       接受风险（真实但业务决定不修）
--
-- triage_note 处置备注（自由文本，如误报原因）；triaged_at 最后状态变更时刻（open 默认 NULL）。
-- 不引入多用户指派 / 审计流水（单人工具，YAGNI）。

ALTER TABLE finding
    ADD COLUMN status text NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'confirmed', 'fixed', 'false_positive', 'accepted')),
    ADD COLUMN triage_note text,
    ADD COLUMN triaged_at timestamptz;

-- 台账按状态筛选高频（默认视图=待处理），建索引。
CREATE INDEX finding_status_idx ON finding (status);

COMMENT ON COLUMN finding.status IS
    'triage 处置态：open/confirmed/fixed/false_positive/accepted。write_finding 落库默认 open。';
