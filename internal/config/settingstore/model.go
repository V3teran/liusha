// Package settingstore 是系统业务旋钮（会话压缩 / 运行时 / 代理流量过滤三组）的多级缓存读写层，
// 构建在资源无关的 cachestore 内核之上：内存 L1（本进程）→ redis L2（跨进程共享 + 失效总线）
// → DB（事实源，system_setting 分组 KV 表）。
//
// 为何分层：api / runner / proxy 是**多进程**。前端在 api 改系统配置后，runner 的会话历史压缩、
// 工具超时等消费点，及 proxy 的流量过滤链必须在运行期读到最新值并热改，否则仍按旧参数跑。
// 故写路径写 DB 后经 cachestore 广播失效键，各进程共享的 Subscribe goroutine 收到即清本地
// L1 + L2，下次读回填最新值（proxy 侧再据此原子换过滤链）。
//
// 缓存粒度：三组各一份类型化快照，各走一个哨兵键（镜像 llmstore 的 keyRouting 模式）。
package settingstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 分组键——与 migration 0098 的 CHECK(group_key IN ...) 一一对应。
const (
	groupCompaction  = "compaction"
	groupRuntime     = "runtime"
	groupProxyFilter = "proxy_filter"
)

// CompactionSettings 是 agent 会话历史压缩旋钮（对应 config.HistoryCompactConfig 的活字段）。
type CompactionSettings struct {
	TriggerRatio            float64 `json:"trigger_ratio"`             // 触发阈值占 ContextWindow 比例，(0,1]
	TrailingBudgetRatio     float64 `json:"trailing_budget_ratio"`     // trailing window 占 ContextWindow 比例，(0,1]
	CompactorTimeoutSeconds int     `json:"compactor_timeout_seconds"` // 单次蒸馏 LLM 调用超时秒
}

// RuntimeSettings 是工具运行时 + 沙箱 + 会话的活旋钮集合。
type RuntimeSettings struct {
	StepToolTimeoutSeconds int `json:"step_tool_timeout_seconds"` // 单次工具执行兜底超时上限秒
	RunTailBytes           int `json:"run_tail_bytes"`            // run_command stdout/stderr 截尾字节数
	FindingsLimitInPrompt  int `json:"findings_limit_in_prompt"`  // agent prompt 注入既有 finding 的 DB 读上限
}

// ProxyFilterSettings 是代理流量过滤责任链规则（对应 config.ProxyConfig 的过滤字段）。
// 运行期改这里 → 各进程失效 → proxy 重建过滤链原子替换（真热改）。
type ProxyFilterSettings struct {
	AllowHosts              []string `json:"allow_hosts"`               // 非空则仅放行这些 host（白名单）
	ExcludeMethods          []string `json:"exclude_methods"`           // 排除的 HTTP 方法（OPTIONS/HEAD/CONNECT…）
	ExcludeHosts            []string `json:"exclude_hosts"`             // 排除的 host（黑名单）
	ExcludeUpgradeProtocols []string `json:"exclude_upgrade_protocols"` // 排除的 Upgrade 协议（websocket…）
	ExcludeSuffixes         []string `json:"exclude_suffixes"`          // 排除的 URL 后缀（.css/.js/图片…）
	ExcludeContentTypes     []string `json:"exclude_content_types"`     // 排除的 Content-Type
	ExcludeStatusCodes      []int    `json:"exclude_status_codes"`      // 排除的响应状态码
	MaxRequestBodySize      int      `json:"max_request_body_size"`     // 请求体切片上限字节
	MaxResponseBodySize     int      `json:"max_response_body_size"`    // 响应体切片上限字节
}

// normalized 把 nil 切片归一为空切片：DB 存的 JSON null 反序列化回 nil，
// 但过滤链构建与前端编辑都期望空数组语义（无规则 ≠ 缺字段）。
func (p ProxyFilterSettings) normalized() ProxyFilterSettings {
	if p.AllowHosts == nil {
		p.AllowHosts = []string{}
	}
	if p.ExcludeMethods == nil {
		p.ExcludeMethods = []string{}
	}
	if p.ExcludeHosts == nil {
		p.ExcludeHosts = []string{}
	}
	if p.ExcludeUpgradeProtocols == nil {
		p.ExcludeUpgradeProtocols = []string{}
	}
	if p.ExcludeSuffixes == nil {
		p.ExcludeSuffixes = []string{}
	}
	if p.ExcludeContentTypes == nil {
		p.ExcludeContentTypes = []string{}
	}
	if p.ExcludeStatusCodes == nil {
		p.ExcludeStatusCodes = []int{}
	}
	return p
}

// dbStore 封装 system_setting 分组 KV 表的 JSONB 持久化。
// 运行期消费不直连本类型，走 Store（多级缓存 + 跨进程失效）。
type dbStore struct {
	pool *pgxpool.Pool
}

// newDBStore 用 pgxpool 构造 dbStore。
func newDBStore(pool *pgxpool.Pool) *dbStore { return &dbStore{pool: pool} }

// getGroup 读一个分组的 JSONB 并反序列化为 T。缺行时 %w 包 pgx.ErrNoRows（供上层判缺省兜底）。
func getGroup[T any](ctx context.Context, pool *pgxpool.Pool, group string) (T, error) {
	var zero T
	var raw []byte
	row := pool.QueryRow(ctx, "SELECT value FROM system_setting WHERE group_key=$1", group)
	if err := row.Scan(&raw); err != nil {
		return zero, fmt.Errorf("get setting %q: %w", group, err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, fmt.Errorf("get setting %q: unmarshal: %w", group, err)
	}
	return v, nil
}

// saveGroup upsert 一个分组的 JSONB 快照（存在则覆盖）。
func saveGroup[T any](ctx context.Context, pool *pgxpool.Pool, group string, v T) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("save setting %q: marshal: %w", group, err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO system_setting (group_key, value)
		VALUES ($1, $2)
		ON CONFLICT (group_key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`,
		group, raw)
	if err != nil {
		return fmt.Errorf("save setting %q: %w", group, err)
	}
	return nil
}

func (s *dbStore) GetCompaction(ctx context.Context) (CompactionSettings, error) {
	return getGroup[CompactionSettings](ctx, s.pool, groupCompaction)
}
func (s *dbStore) SaveCompaction(ctx context.Context, v CompactionSettings) error {
	return saveGroup(ctx, s.pool, groupCompaction, v)
}

func (s *dbStore) GetRuntime(ctx context.Context) (RuntimeSettings, error) {
	return getGroup[RuntimeSettings](ctx, s.pool, groupRuntime)
}
func (s *dbStore) SaveRuntime(ctx context.Context, v RuntimeSettings) error {
	return saveGroup(ctx, s.pool, groupRuntime, v)
}

func (s *dbStore) GetProxyFilter(ctx context.Context) (ProxyFilterSettings, error) {
	v, err := getGroup[ProxyFilterSettings](ctx, s.pool, groupProxyFilter)
	if err != nil {
		return ProxyFilterSettings{}, err
	}
	return v.normalized(), nil
}
func (s *dbStore) SaveProxyFilter(ctx context.Context, v ProxyFilterSettings) error {
	return saveGroup(ctx, s.pool, groupProxyFilter, v.normalized())
}

// isNotFound 判定底层「不存在」——getGroup 用 %w 包 pgx.ErrNoRows。
func isNotFound(err error) bool { return err != nil && errors.Is(err, pgx.ErrNoRows) }
