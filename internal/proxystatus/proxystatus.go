// Package proxystatus 是代理的运行状态：proxy 组件定期上报，api 读取后告诉控制台代理是否在运行、
// 客户端该连哪个地址、该信任哪张根证书。
//
// 状态存在 Redis 的一条记录里：三种部署方式都能读到，api 不必与 proxy 共享证书目录；下载到的
// 根证书也一定是正在运行的代理实际使用的那一张。记录不设过期：代理停掉后控制台仍能显示最后在线
// 时间、仍能下载证书；是否在线按心跳时间判断。系统只运行一个 proxy，记录只有一条。
package proxystatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// key 是状态记录的 Redis 键。
const key = "liusha:proxy:status"

// HeartbeatInterval 是 proxy 上报状态的周期。每次上报都写入完整记录：Redis 被清空后一个周期内恢复。
const HeartbeatInterval = 10 * time.Second

// offlineAfter 是心跳中断多久判为离线：容忍两次上报失败。
const offlineAfter = 3 * HeartbeatInterval

// Record 是代理的运行状态。
type Record struct {
	StartedAt   time.Time `json:"started_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	// StoppedAt 是正常退出的时刻；进程被强杀时为空，靠心跳超时判离线。
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	// LastRequestAt 是最近收到请求的时刻（被过滤掉的也算）；尚未收到过时为空。
	LastRequestAt *time.Time `json:"last_request_at,omitempty"`
	// PublicAddr 是客户端设置代理时填写的地址：「主机:端口」，或只有「:端口」（主机即控制台的主机）。
	PublicAddr string `json:"public_addr"`
	// CAPEM 是根证书（PEM，不含私钥）；CAFingerprint 是它的 SHA-256 指纹。
	CAPEM         string `json:"ca_pem"`
	CAFingerprint string `json:"ca_fingerprint"`
}

// Online 判断代理此刻是否在运行：没有正常退出，且心跳未中断。
func (r Record) Online(now time.Time) bool {
	return r.StoppedAt == nil && now.Sub(r.HeartbeatAt) <= offlineAfter
}

// Store 读写状态记录。
type Store struct {
	rdb *redis.Client
}

// New 构造 Store。
func New(rdb *redis.Client) *Store { return &Store{rdb: rdb} }

// Put 写入完整记录（覆盖）。
func (s *Store) Put(ctx context.Context, r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("编码代理状态: %w", err)
	}
	if err := s.rdb.Set(ctx, key, b, 0).Err(); err != nil {
		return fmt.Errorf("写入代理状态: %w", err)
	}
	return nil
}

// Get 读取记录；proxy 从未上报过时 ok 为 false。
func (s *Store) Get(ctx context.Context) (r Record, ok bool, err error) {
	b, err := s.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("读取代理状态: %w", err)
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Record{}, false, fmt.Errorf("解析代理状态: %w", err)
	}
	return r, true, nil
}
