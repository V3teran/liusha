-- 0108: 攻击图世界模型（L3 状态层）—— 多域自主渗透重构的地基。
--
-- 动机：现存 finding.host（NOT NULL + CHECK + dedup_key 生成列）、credential（按 host 索引 +
--   仅 HTTP headers/query/body 注入位）、lead（Redis key 按 host）三处各自绑死单 host + HTTP，
--   物理上无法表示二进制（无 host）、云（资产是 ARN）、多阶段横向（多主机 + 立足点）。
--
-- 业界实践：BloodHound（AD 攻击图）、Incalmo（attack-state 抽象）、ARTEX（exploration graph）
--   持久化的都是「已确证的世界状态」（资产/身份/可达性），不是过程细节。本表遵循同一边界：
--   只记结果状态里程碑，绝不记过程（gdb 单步、每条 HTTP 报文归 investigation-trace 投影 + traffic 存储）。
--
-- 设计：统一单表 wm_node + 判别列 kind（非每类一张表）——让 wm_edge 的外键统一、加新节点种类不改表。
--   目标标识用多态三元组 (domain, ref_kind, locator) 取代裸 host；locator 语义仅由对应 Domain Profile
--   解释，核心永不 parse。凭据密文走应用层 cryptx AES-GCM 加密后落 attrs jsonb，明文永不落库。
--
-- 节点 5 类：target / asset / credential / access / finding
--   （lead / observation 是在途认知，留 Redis 工作记忆，Verifier 晋升后才进本表）。
-- 边 3 类：derives（认知因果）/ enables（能力使能，攻击链=enables 路径）/ on（归属附着）。

CREATE TABLE wm_node (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scan_id     text NOT NULL,                       -- 图归属（一次交战一个图）
    kind        text NOT NULL
                CHECK (kind IN ('target', 'asset', 'credential', 'access', 'finding')),

    -- 多态目标标识（契约一），取代裸 host。locator 仅对应 Profile 解释。
    domain      text NOT NULL,                       -- web|binary|cloud|host，由 Profile 定义
    ref_kind    text NOT NULL,                       -- endpoint|file|resource|node|...
    locator     text NOT NULL,                       -- 域内寻址

    -- 状态载荷：kind 决定形状，Profile 决定 evidence 细节（密文字段在此加密存储）。
    attrs       jsonb NOT NULL DEFAULT '{}',

    -- 确证元数据（Verifier 晋升门产物）：图里只允许 confirmed / assumed。
    confidence  text NOT NULL DEFAULT 'assumed'
                CHECK (confidence IN ('confirmed', 'assumed')),
    verified_by uuid,                                -- 指向确证它的 wm_verification.id

    seq         bigserial,                           -- 对外稳定短号（沿用 finding.seq 好设计）
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    -- 幂等：同图内 (kind, domain, ref_kind, locator) 唯一 —— 显式稳定键，
    -- 废弃 finding「summary 前 60 字」脆弱 dedup（0048）。
    UNIQUE (scan_id, kind, domain, ref_kind, locator)
);
CREATE INDEX wm_node_scan_kind_idx ON wm_node (scan_id, kind);

COMMENT ON TABLE wm_node IS
    '攻击图世界模型节点（L3 状态层）：只存已确证/假定的世界状态，过程细节归 investigation-trace。';
COMMENT ON COLUMN wm_node.locator IS
    '域内寻址，语义仅由对应 Domain Profile 解释；核心永不 parse。';

-- 边：连接任意两节点。攻击链 = enables 边的路径（Credential→Access→Asset→...）。
CREATE TABLE wm_edge (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scan_id    text NOT NULL,
    rel        text NOT NULL CHECK (rel IN ('derives', 'enables', 'on')),
    src        uuid NOT NULL REFERENCES wm_node(id) ON DELETE CASCADE,
    dst        uuid NOT NULL REFERENCES wm_node(id) ON DELETE CASCADE,
    attrs      jsonb NOT NULL DEFAULT '{}',          -- 如 enables 上标注「用哪个原语使能」
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (scan_id, rel, src, dst)
);
CREATE INDEX wm_edge_src_idx ON wm_edge (src, rel);
CREATE INDEX wm_edge_dst_idx ON wm_edge (dst, rel);

COMMENT ON TABLE wm_edge IS
    '世界模型边：derives（认知因果）/ enables（能力使能，攻击链）/ on（归属附着）。';

-- Verifier 晋升门取证记录：Lead → 图节点 的每次复检落一行，是可复现交付 + 合规审计的证据链。
CREATE TABLE wm_verification (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scan_id    text NOT NULL,
    lead_id    text NOT NULL,                        -- 被验证的工作记忆 Lead（Redis 侧 id）
    primitives jsonb NOT NULL DEFAULT '[]',          -- 回放了哪些 L1 原语
    outcome    text NOT NULL CHECK (outcome IN ('confirmed', 'refuted')),
    evidence   jsonb NOT NULL DEFAULT '{}',          -- 复现证据（截图/crash/api 响应）
    duration_ms bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX wm_verification_scan_idx ON wm_verification (scan_id, created_at DESC);

COMMENT ON TABLE wm_verification IS
    'Verifier 晋升门取证：outcome=confirmed 才允许 Lead 晋升为 wm_node 并置 confidence=confirmed。';
