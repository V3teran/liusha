package configstore

import "sync"

// l1Cache 是进程内第一级缓存：RWMutex 保护的 key→json 字节表。
// 无 TTL——条目靠失效消息（本进程写 / redis 订阅）驱逐，不靠过期。
// 值统一存 json 字节（与 L2 同形），读路径按需 Unmarshal。
type l1Cache struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func newL1() *l1Cache { return &l1Cache{m: make(map[string][]byte)} }

// get 返回键对应的 json 字节副本命中标志。
func (c *l1Cache) get(key string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	b, ok := c.m[key]
	return b, ok
}

// set 存入 val 的副本，避免调用方复用底层数组污染缓存。
func (c *l1Cache) set(key string, val []byte) {
	cp := make([]byte, len(val))
	copy(cp, val)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = cp
}

// del 删除一批键（失效驱逐）。
func (c *l1Cache) del(keys ...string) {
	if len(keys) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		delete(c.m, k)
	}
}
