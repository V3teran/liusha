package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// TrafficDeduplicator 基于内容哈希 + 时间窗口的流量去重器。
//
//	设计：hash → firstSeen 时间戳；查询时若窗口内已存在则视为重复。
//	并发：RWMutex 保护 map（CalculateHash 是无状态的，无锁）。
type TrafficDeduplicator struct {
	mu      sync.RWMutex
	hashMap map[string]int64
}

// NewTrafficDeduplicator 创建空的去重器。
func NewTrafficDeduplicator() *TrafficDeduplicator {
	return &TrafficDeduplicator{
		hashMap: make(map[string]int64),
	}
}

// CalculateHash 用 sha256(method + host + uri + body) 算稳定哈希。
// 输入归一化为字符串拼接（| 作分隔），避免边界混淆。
func (d *TrafficDeduplicator) CalculateHash(snap *TrafficSnapshot) string {
	h := sha256.New()
	h.Write([]byte(snap.Method))
	h.Write([]byte("|"))
	h.Write([]byte(snap.Host))
	h.Write([]byte("|"))
	h.Write([]byte(snap.URI))
	h.Write([]byte("|"))
	h.Write(snap.RequestBody)
	return hex.EncodeToString(h.Sum(nil))
}

// IsDuplicate 判断 hash 在 dedupeWindow 秒内是否重复。
//
//	首次见到 → 记录 ts，返 false
//	窗口内重复 → 返 true（不更新 ts，保留首见时间，便于 Cleanup 准确过期）
//	窗口外再见 → 当作新记录，更新 ts，返 false
func (d *TrafficDeduplicator) IsDuplicate(hash string, ts int64, dedupeWindow int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	firstSeen, exists := d.hashMap[hash]
	if !exists {
		d.hashMap[hash] = ts
		return false
	}
	if ts-firstSeen < dedupeWindow {
		return true
	}
	// 窗口外：刷新为新窗口的起点
	d.hashMap[hash] = ts
	return false
}

// Cleanup 删除窗口外的过期 hash，返回清理数量。
func (d *TrafficDeduplicator) Cleanup(now int64, dedupeWindow int64) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	cleaned := 0
	for hash, firstSeen := range d.hashMap {
		if now-firstSeen >= dedupeWindow {
			delete(d.hashMap, hash)
			cleaned++
		}
	}
	return cleaned
}

// Size 返回当前记录数（O(1)）。
func (d *TrafficDeduplicator) Size() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.hashMap)
}
