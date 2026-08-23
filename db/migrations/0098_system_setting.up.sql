-- 0098: 系统业务旋钮迁 DB——会话压缩 / 运行时 / 代理流量过滤三组标量集合。
-- 取代 config.yaml 里散落的 compaction.history_compact / toolruntime / sandbox / session /
-- proxy.filter 等业务参数：DB 成事实源，前端「系统配置模块」改这些行，api/runner/proxy
-- 经多级缓存运行期热读（改一处、跨进程即时生效，无需重启进程）。
--
-- 为何用单张分组 KV（而非 llm_config 那样的强类型多表）：这些旋钮是异构标量集合
-- （比率 / 超时秒 / 字节上限 / 字符串数组），无实体关系、无外键、低频改，强类型多表得罪
-- 靠不住的迁移成本换不来收益。按分组一行、value 存 JSONB 类型化快照最简（KISS/YAGNI）。
-- settingstore 读时按各 sentinel key 反序列化成类型化 struct（镜像 llmstore 的 keyRouting 模式）。
--
-- 边界：仅业务旋钮入此表。基础设施参数（连接池大小 / 端口 / 并发度 / 超时上限等启动期
-- 一次性装配、无跨进程一致性需求）不迁——仍留 yaml。

CREATE TABLE system_setting (
    group_key  text        NOT NULL PRIMARY KEY
               CHECK (group_key IN ('compaction', 'runtime', 'proxy_filter')),
    value      jsonb       NOT NULL,                      -- 该分组的类型化快照 JSON
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE system_setting IS '系统业务旋钮（分组 KV，DB 为事实源，运行期多级缓存热读）；基础设施参数不入此表';
COMMENT ON COLUMN system_setting.group_key IS '分组键：compaction（会话压缩）/ runtime（工具运行时+沙箱+会话）/ proxy_filter（代理流量过滤规则）';
COMMENT ON COLUMN system_setting.value IS '该分组类型化快照的 JSONB；settingstore 按 group 反序列化为 struct';
