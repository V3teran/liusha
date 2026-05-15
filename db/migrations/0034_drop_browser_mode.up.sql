-- 0034: 删除 engagement.mode 的 browser 选项（未来再支持时改回）
--
-- 背景：v0033 把 engagement 升级为渗透会话语义后，proxy 模式（多 host + 24h TTL）
-- 是当前唯一被代码使用的形态。browser 模式（单 host + 无 TTL）的 Store API
-- 和 ModeBrowser 常量虽然就绪，但生产路径没有任何调用方——属于过早设计。
-- 删除 schema 层的 'browser' 选项收紧约束，避免误插。
--
-- mode 字段本身保留（不删列），未来要恢复 browser 时改回 check constraint 即可。

ALTER TABLE engagement DROP CONSTRAINT engagement_mode_check;
ALTER TABLE engagement ADD CONSTRAINT engagement_mode_check CHECK (mode = 'proxy');
