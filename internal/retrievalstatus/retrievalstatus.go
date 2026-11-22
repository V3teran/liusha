// Package retrievalstatus 是检索模型的运行健康状态：语义检索最近一次尝试失败时记录原因，
// 供知识库页把检索方式标识显示为「已降级」——配置仍在（密钥还在），但实际请求一直在退
// 字面检索。api 与 runner 都可写（谁的语义调用失败谁记，跨进程可见）。
//
// 记录存 Redis 并带过期：故障消失后不再有人写，记录自动淡出，页面回到「语义检索」；
// 修复密钥后的第一次成功调用会主动清除记录。没有语义尝试（密钥未配置）时无记录。
package retrievalstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// key 是降级记录的 Redis 键。
const key = "liusha:retrieval:degraded"

// TTL 是降级记录的存活时长：故障持续期间每次失败都刷新（记录一直在），故障消失且无人
// 再触发语义调用后，记录到时淡出。语义调用以任务节奏发生（active 任务每轮规划都会检索），
// 30 分钟足以覆盖「任务跑着、钱没了」的观察窗口。
const TTL = 30 * time.Minute

// Record 是一次语义检索失败的记录。
type Record struct {
	Reason string    `json:"reason"` // 最近一次失败的原因（上游报错原文）
	At     time.Time `json:"at"`     // 记录时刻（最近一次失败的时间）
}

// Store 读写降级记录。
type Store struct {
	rdb *redis.Client
}

// New 构造 Store。
func New(rdb *redis.Client) *Store { return &Store{rdb: rdb} }

// Fail 记录一次语义检索失败（刷新 TTL）。
func (s *Store) Fail(ctx context.Context, reason string) error {
	b, err := json.Marshal(Record{Reason: reason, At: time.Now()})
	if err != nil {
		return fmt.Errorf("编码检索降级记录: %w", err)
	}
	if err := s.rdb.Set(ctx, key, b, TTL).Err(); err != nil {
		return fmt.Errorf("写入检索降级记录: %w", err)
	}
	return nil
}

// Recover 清除降级记录（语义检索恢复）。记录本就不存在时静默成功。
func (s *Store) Recover(ctx context.Context) error {
	if err := s.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("清除检索降级记录: %w", err)
	}
	return nil
}

// Get 读取降级记录；无记录（健康 / 从未失败 / 已淡出）时 ok 为 false。
func (s *Store) Get(ctx context.Context) (Record, bool, error) {
	b, err := s.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("读取检索降级记录: %w", err)
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return Record{}, false, fmt.Errorf("解析检索降级记录: %w", err)
	}
	return r, true, nil
}

// Sink 实现 corpus.SemanticHealth：把语义调用的成败落到降级记录。
// 进程内按状态变化去重——健康路径（绝大多数调用）不发任何 Redis 操作；失败路径每次都写
// （刷新 TTL，持续故障期间记录不会过期）。Redis 故障只记日志、绝不阻塞检索本身。
type Sink struct {
	store   *Store
	logger  zerolog.Logger
	failing atomic.Bool
}

// NewSink 构造 corpus.SemanticHealth 的实现（api 与 runner 装配共用）。
func NewSink(rdb *redis.Client, logger zerolog.Logger) *Sink {
	return &Sink{store: New(rdb), logger: logger}
}

// Degraded 记录语义调用失败：每次都写（刷新 TTL），同进程状态去重只省成功路径。
func (h *Sink) Degraded(ctx context.Context, reason string) {
	h.failing.Store(true)
	if err := h.store.Fail(ctx, reason); err != nil {
		h.logger.Warn().Err(err).Msg("写入检索降级记录失败（页面可能看不到降级标识）")
	}
}

// Healthy 记录语义调用恢复：只在「降级 → 健康」变化时清一次记录，常规成功路径零操作。
func (h *Sink) Healthy(ctx context.Context) {
	if !h.failing.CompareAndSwap(true, false) {
		return
	}
	if err := h.store.Recover(ctx); err != nil {
		// 恢复失败：把状态拨回去，下次成功调用重试清除
		h.failing.Store(true)
		h.logger.Warn().Err(err).Msg("清除检索降级记录失败（下轮成功调用重试）")
	}
}
