package db

import "testing"

// INFO 的分节标题与空行跳过，值里的冒号保留（如 executable 路径在 Windows 上带盘符）。
func TestParseRedisInfo(t *testing.T) {
	got := parseRedisInfo("# Server\r\nredis_version:8.0.2\r\nexecutable:C:/redis/redis-server\r\n\r\n# Memory\r\nused_memory:3145728\r\nmaxmemory:0\r\n")
	want := map[string]string{
		"redis_version": "8.0.2",
		"executable":    "C:/redis/redis-server",
		"used_memory":   "3145728",
		"maxmemory":     "0",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q，期望 %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("多出的键: %v", got)
	}
}
