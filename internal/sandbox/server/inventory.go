package server

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/sandbox/browser"
)

// inventoryTTL 是安装状态的缓存期：浏览器能力探测要拉起 Python 导入依赖（秒级），而工具页与
// 每个任务开始时都会查询；安装状态变化以分钟计，一分钟的滞后可以接受。
const inventoryTTL = time.Minute

// inventoryCache 探测并缓存沙盒的安装状态。
type inventoryCache struct {
	tools  []string
	python string

	mu      sync.Mutex
	value   sandbox.Inventory
	expires time.Time
}

func newInventoryCache(tools []string, python string) *inventoryCache {
	return &inventoryCache{tools: tools, python: python}
}

func (c *inventoryCache) get(ctx context.Context) sandbox.Inventory {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires) {
		return c.value
	}
	c.value = probe(ctx, c.tools, c.python)
	c.expires = time.Now().Add(inventoryTTL)
	return c.value
}

// probe 探测工具清单中每个 CLI 工具是否在 PATH 中可执行，以及浏览器能力。
func probe(ctx context.Context, tools []string, python string) sandbox.Inventory {
	inv := sandbox.Inventory{Tools: make(map[string]bool, len(tools))}
	for _, name := range tools {
		_, err := exec.LookPath(name)
		inv.Tools[name] = err == nil
	}
	if python == "" {
		inv.Browser = sandbox.BrowserStatus{Detail: "未配置 sandbox.browser_python"}
		return inv
	}
	st := browser.Probe(ctx, python)
	inv.Browser = sandbox.BrowserStatus{Installed: st.Installed, Detail: st.Detail}
	return inv
}
