// Package notes 实现 hunter 短期工作笔记的 Redis 共享存储。
//
// 短期记忆 vs 长期记忆 边界：
//   - notes (本包)：engagement 内同 host 跨 task 共享，TTL 自动过期，不入 PG。
//   - lesson (internal/lesson)：跨 engagement / 按 host 持久化长期经验，PG。
//   - finding (internal/finding)：漏洞 PoC 结论，PG。
//
// key=liusha:note:{engagement_id}，LIST 类型；每条 element 是 JSON bytes
// （形如 {"content":"...","agent_run_id":"..."}），store 不解析也不强制结构——
// write_note 工具层负责语义。
package notes

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store 是 hunter 短期工作笔记的最小读写接口。
//
// 同时被 internal/tools/common/note.go 的 NoteStore 与
// internal/react/reviewer_llm.go 的 NotesReader 隐式满足。
type Store interface {
	AppendNote(ctx context.Context, engagementID string, entry []byte) error
	ReadNotes(ctx context.Context, engagementID string) ([]byte, error)
}

// Config 控制 RedisStore 行为，由 yaml notes 节注入。
//
// 单一闸 MaxEntries：AppendNote 后 LTRIM 末尾 N 条；ReadNotes 取全部
// （反正已被 LTRIM 限到 N 条以内），无两层截断、无中间死区。
// TTL：仅在 key 首次创建时设一次（ExpireNX），后续 AppendNote **不刷新**——
// 让 notes 寿命与 engagement.MaxAge 严格对齐：从 key 创建时刻起算固定 24h
// 到期，而不是"24h 无写入"才过期。
type Config struct {
	KeyPrefix  string
	MaxEntries int
	TTL        time.Duration
}

// fallback 是 Config 字段零值时的兜底常量。
const (
	fallbackKeyPrefix  = "liusha:note:"
	fallbackMaxEntries = 200
	fallbackTTL        = 24 * time.Hour
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
	return c
}

// RedisStore 用 Redis LIST 实现 notes 短期共享存储。
type RedisStore struct {
	client *redis.Client
	cfg    Config
}

// NewRedis 构造 RedisStore；client 必填，cfg 字段为 0 走兜底常量。
func NewRedis(client *redis.Client, cfg Config) *RedisStore {
	return &RedisStore{client: client, cfg: cfg.effective()}
}

func (s *RedisStore) key(engagementID string) string {
	return s.cfg.KeyPrefix + engagementID
}

// AppendNote 追加一条 entry 到 engagement notes 列表末尾，滚动裁剪并
// 在 key 首次创建时设 TTL（之后不刷新）。
//
// 三步走 pipeline：RPUSH + LTRIM 保留末尾 MaxEntries 条 + ExpireNX 仅首次设 TTL。
// 使用 TxPipeline 让三个命令在同一 round-trip 完成且 atomic。
// ExpireNX 语义：当 key 没有 TTL 时（首次 RPUSH 后）设 TTL；已有 TTL 时不动——
// 让 notes 寿命与 engagement.MaxAge 严格对齐（从 key 创建时刻起算固定 24h）。
func (s *RedisStore) AppendNote(ctx context.Context, engagementID string, entry []byte) error {
	if engagementID == "" {
		return fmt.Errorf("AppendNote: engagementID 不能为空")
	}
	if len(entry) == 0 {
		return fmt.Errorf("AppendNote: entry 不能为空")
	}
	k := s.key(engagementID)
	pipe := s.client.TxPipeline()
	pipe.RPush(ctx, k, entry)
	pipe.LTrim(ctx, k, int64(-s.cfg.MaxEntries), -1)
	pipe.ExpireNX(ctx, k, s.cfg.TTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("append note %s: %w", k, err)
	}
	return nil
}

// ReadNotes 读全部 entry（已被 LTRIM 限到 MaxEntries 条以内），
// 包成 {"notes":[<raw entry>,...]} 返回。
//
// 输出格式与旧 engagement.Store.ReadNotes 保持一致——hunter/skill.go
// 与 reviewer_llm.go 已按此结构 Unmarshal，切换后零改动。
//
// key 不存在返回空数组 {"notes":[]}，不报错——caller 已按"无数据"路径处理。
func (s *RedisStore) ReadNotes(ctx context.Context, engagementID string) ([]byte, error) {
	if engagementID == "" {
		return nil, fmt.Errorf("ReadNotes: engagementID 不能为空")
	}
	k := s.key(engagementID)
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
