// Package notes 实现 hunter 短期工作笔记的 Redis 共享存储。
//
// 短期记忆 vs 长期记忆 边界：
//   - notes (本包)：owner 内同 host 跨 task 共享，TTL 自动过期，不入 PG。
//   - lesson (internal/lesson)：跨 owner / 按 host 持久化长期经验，PG。
//   - finding (internal/finding)：漏洞 PoC 结论，PG。
//
//  owner 可挂多 host（passive 模式接受任意 host 流量），notes 按
// (owner_id, host) 二维切分——host A 的 fact 不会污染 host B。
//
// key=liusha:note:{owner_id}:{host}，LIST 类型；每条 element 是 JSON bytes
// （形如 {"content":"...","agent_run_id":"..."}），store 不解析也不强制结构——
// write_note 工具层负责语义。
//
// 容量管理：超过 CompactThreshold（默认 200）时调 Compactor（默认 LLM 蒸馏）把
// 前 CompactBatchSize（默认 100）条压缩成 1 条 summary，保留早期信息语义。
// Compactor 失败时退化为 LTRIM 末尾 MaxEntries 兜底，永不阻断 AppendNote。
// 每个 (owner, host) 独立蒸馏阈值 / 独立锁 / 独立 TTL。
package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// Store 是 hunter 短期工作笔记的最小读写接口。
//
// 同时被 internal/tools/common/note.go 的 NoteStore 与
// internal/react/inspector_llm.go 的 NotesReader 隐式满足。
// 所有方法带 host 参数——同 owner 多 host 切分隔离。
type Store interface {
	AppendNote(ctx context.Context, ownerID, host string, entry []byte) error
	ReadNotes(ctx context.Context, ownerID, host string) ([]byte, error)
}

// Compactor 是 notes 蒸馏接口。
//
// Compact 把 N 条老 entry 蒸馏成 1 条 summary entry（合规 JSON）。
// ctx 含 deadline；超时/失败返回 err，RedisStore 退化为 LTRIM。
type Compactor interface {
	Compact(ctx context.Context, oldEntries []json.RawMessage) ([]byte, error)
}

// Config 控制 RedisStore 行为，由 yaml notes 节注入。
type Config struct {
	KeyPrefix  string
	MaxEntries int           // 兜底硬上限：Compactor 失败时 LTRIM 保留末尾 N 条
	TTL        time.Duration // 仅在 key 首次创建时设（ExpireNX）

	// 蒸馏触发：LLEN > CompactThreshold 且 Compactor 非 nil 时触发
	CompactThreshold int
	CompactBatchSize int           // 每次蒸馏前 N 条
	CompactTimeout   time.Duration // 单次蒸馏（含 LLM 调用）超时
}

const (
	fallbackKeyPrefix        = "liusha:note:"
	fallbackMaxEntries       = 200
	fallbackTTL              = 24 * time.Hour
	fallbackCompactThreshold = 200
	fallbackCompactBatchSize = 100
	fallbackCompactTimeout   = 30 * time.Second
	compactLockTTL           = 30 * time.Second
	compactLockPrefix        = "lock:compact:"
)

func (c Config) effective() Config {
	if c.KeyPrefix == "" {
		c.KeyPrefix = fallbackKeyPrefix
	}
	if c.MaxEntries <= 0 {
		c.MaxEntries = fallbackMaxEntries
	}
	if c.TTL <= 0 {
		c.TTL = fallbackTTL
	}
	if c.CompactThreshold <= 0 {
		c.CompactThreshold = fallbackCompactThreshold
	}
	if c.CompactBatchSize <= 0 {
		c.CompactBatchSize = fallbackCompactBatchSize
	}
	if c.CompactTimeout <= 0 {
		c.CompactTimeout = fallbackCompactTimeout
	}
	return c
}

// RedisStore 用 Redis LIST 实现 notes 短期共享存储。
type RedisStore struct {
	client    *redis.Client
	cfg       Config
	compactor Compactor // 可空——nil 时退化为 LTRIM 行为
	logger    zerolog.Logger
}

// NewRedis 构造 RedisStore；client 必填，cfg 字段为 0 走兜底常量。
// 默认不带 Compactor，调用方需通过 WithCompactor 注入（生产环境推荐）。
func NewRedis(client *redis.Client, cfg Config) *RedisStore {
	return &RedisStore{client: client, cfg: cfg.effective()}
}

// WithCompactor 注入蒸馏器。nil 入参等价于不注入（退化为 LTRIM）。
// 链式返回 *RedisStore 以便装配处一行串联。
func (s *RedisStore) WithCompactor(c Compactor) *RedisStore {
	s.compactor = c
	return s
}

// WithLogger 注入 logger，给 fallbackTrim 等边缘失败路径输出 warn。零值 logger 不输出。
func (s *RedisStore) WithLogger(l zerolog.Logger) *RedisStore {
	s.logger = l
	return s
}

// key 拼成 liusha:note:{ownerID}:{host}——每个 (owner, host) 独立 LIST。
func (s *RedisStore) key(ownerID, host string) string {
	return s.cfg.KeyPrefix + ownerID + ":" + host
}

// AppendNote 追加一条 entry 到 (owner, host) notes 列表末尾，
// 刷新 TTL 并按需触发蒸馏。
//
// pipeline：RPUSH + ExpireNX + LLEN（一次 round-trip）。
// LLEN 超过 CompactThreshold 时同步触发蒸馏（best-effort，失败 fallback LTRIM）。
// 同步触发理由：触发频率低（几小时一次），阻塞 hunter 1-3s 可接受。
func (s *RedisStore) AppendNote(ctx context.Context, ownerID, host string, entry []byte) error {
	if ownerID == "" {
		return fmt.Errorf("AppendNote: ownerID 不能为空")
	}
	if host == "" {
		return fmt.Errorf("AppendNote: host 不能为空")
	}
	if len(entry) == 0 {
		return fmt.Errorf("AppendNote: entry 不能为空")
	}
	k := s.key(ownerID, host)

	pipe := s.client.TxPipeline()
	pipe.RPush(ctx, k, entry)
	pipe.ExpireNX(ctx, k, s.cfg.TTL)
	llenCmd := pipe.LLen(ctx, k)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("append note %s: %w", k, err)
	}

	if llenCmd.Val() > int64(s.cfg.CompactThreshold) {
		if s.compactor != nil {
			s.tryCompact(ctx, k)
		} else {
			// 安全网：未注入 Compactor 时直接 LTRIM 兜底，防 key 无限增长
			s.fallbackTrim(ctx, k)
		}
	}
	return nil
}

// tryCompact 是 best-effort 同步蒸馏：
//  1. SETNX 锁防并发蒸馏
//  2. LRANGE 前 CompactBatchSize 条
//  3. 调 Compactor 拿 summary
//  4. Lua 原子 LTRIM batchSize -1 + LPUSH summary（一次 round-trip）
//
// 任何步骤失败 → 退化为 LTRIM 末尾 MaxEntries 兜底，保证 key 不会无限增长。
// 不返回 error——caller 不应感知蒸馏失败。
func (s *RedisStore) tryCompact(parentCtx context.Context, k string) {
	lockKey := compactLockPrefix + k
	locked, err := s.client.SetNX(parentCtx, lockKey, "1", compactLockTTL).Result()
	if err != nil || !locked {
		return
	}
	defer s.client.Del(parentCtx, lockKey)

	ctx, cancel := context.WithTimeout(parentCtx, s.cfg.CompactTimeout)
	defer cancel()

	batch := int64(s.cfg.CompactBatchSize)
	olds, err := s.client.LRange(ctx, k, 0, batch-1).Result()
	if err != nil || len(olds) == 0 {
		s.fallbackTrim(parentCtx, k)
		return
	}

	rawOlds := make([]json.RawMessage, len(olds))
	for i, o := range olds {
		rawOlds[i] = json.RawMessage(o)
	}

	summary, err := s.compactor.Compact(ctx, rawOlds)
	if err != nil || len(summary) == 0 {
		s.fallbackTrim(parentCtx, k)
		return
	}

	// Lua 原子：LTRIM 删前 batch 条（保留 batch 之后），LPUSH summary 到头部。
	script := redis.NewScript(`
		redis.call('LTRIM', KEYS[1], tonumber(ARGV[1]), -1)
		redis.call('LPUSH', KEYS[1], ARGV[2])
		return 1
	`)
	if _, err := script.Run(parentCtx, s.client, []string{k}, batch, summary).Result(); err != nil {
		s.fallbackTrim(parentCtx, k)
	}
}

// fallbackTrim 蒸馏失败时的兜底：直接 LTRIM 末尾 MaxEntries 条，丢最旧的。
// LTRIM 再失败仅 warn——这是 best-effort 二次兜底，notes key 无限增长会吃掉 LLM context，
// 但生产环境基本不会同时遇到"蒸馏失败 + LTRIM 失败"。
func (s *RedisStore) fallbackTrim(ctx context.Context, k string) {
	if err := s.client.LTrim(ctx, k, int64(-s.cfg.MaxEntries), -1).Err(); err != nil {
		s.logger.Warn().Err(err).Str("key", k).Msg("notes fallbackTrim LTRIM 失败（key 可能无限增长）")
	}
}

// ReadNotes 读 (owner, host) 范围全部 entry，包成 {"notes":[<raw entry>,...]} 返回。
//
// 输出格式与旧 owner store.ReadNotes 保持一致——hunter/skill.go
// 与 inspector_llm.go 已按此结构 Unmarshal。
//
// key 不存在返回空数组 {"notes":[]}，不报错。
func (s *RedisStore) ReadNotes(ctx context.Context, ownerID, host string) ([]byte, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("ReadNotes: ownerID 不能为空")
	}
	if host == "" {
		return nil, fmt.Errorf("ReadNotes: host 不能为空")
	}
	k := s.key(ownerID, host)
	items, err := s.client.LRange(ctx, k, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("read notes %s: %w", k, err)
	}
	raws := make([]json.RawMessage, 0, len(items))
	for _, it := range items {
		raws = append(raws, json.RawMessage(it))
	}
	return json.Marshal(struct {
		Notes []json.RawMessage `json:"notes"`
	}{Notes: raws})
}
