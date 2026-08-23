-- 0091: 建工具目录表 tool——两套工具体系（内置 function / 外置 cli）的统一可查询目录。
--   · 事实源仍是代码：function 工具 = einotools.FunctionToolCatalog；cli 工具 = tools.yaml。
--   · 本表是启动期幂等同步出来的「目录副本」，供前端工具模块检索/分页/展示与智能体选择工具。
-- name 全局唯一即可（两套体系当前无重名；若未来撞名，reconcile 以 kind 消歧写入会因 name UNIQUE 冲突暴露，属预期）。
CREATE TABLE tool (
    name        text        NOT NULL PRIMARY KEY,
    kind        text        NOT NULL CHECK (kind IN ('function','cli')),
    category    text        NOT NULL DEFAULT '',
    description text        NOT NULL DEFAULT '',
    scenarios   jsonb       NOT NULL DEFAULT '[]'::jsonb, -- 仅 cli 工具有值（交战域标签）；function 恒为 []
    sort_order  int         NOT NULL DEFAULT 0,           -- 保留代码内声明顺序，前端稳定展示
    synced_at   timestamptz NOT NULL DEFAULT now()        -- 最近一次 reconcile 同步时间
);

CREATE INDEX tool_kind_idx ON tool (kind, sort_order);

COMMENT ON TABLE tool IS '工具目录（内置 function + 外置 cli）；代码为事实源，启动期幂等同步的可查询副本';
COMMENT ON COLUMN tool.kind IS 'function=进程内原生函数工具；cli=tools.yaml 外置沙箱工具';
COMMENT ON COLUMN tool.scenarios IS 'cli 工具的交战域标签（[web] 等）；function 工具恒为空数组';
