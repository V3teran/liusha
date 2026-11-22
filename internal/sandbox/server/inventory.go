package server

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/sandbox/browser"
)

// inventoryTTL 是安装状态的缓存期：浏览器能力探测要拉起 Python 导入依赖（秒级），而工具页、
// 概览页与每个任务开始时都会查询；安装状态变化以分钟计，一分钟的滞后可以接受。
const inventoryTTL = time.Minute

// inventoryProbeTimeout 是一次探测的上限（浏览器探测自带 30 秒上限，余下留给 CLI 工具探测）。
const inventoryProbeTimeout = time.Minute

// inventoryCache 缓存沙盒的安装状态。
//
// 探测与任何一个请求方的生命周期解耦：它在自己的超时下运行，同一时刻至多一次，结果对所有
// 请求方生效。若绑在发起请求的 ctx 上，一个提前放弃的请求方（超时、断开）会让探测中途失败，
// 「全部未安装」随即被缓存整整一个周期——这期间开始的任务看不到任何工具。
// 过期后先返回旧值、在后台刷新：只有从未探测过时才需要等待。
type inventoryCache struct {
	probe func(context.Context) sandbox.Inventory

	mu       sync.Mutex
	value    sandbox.Inventory
	probed   bool
	expires  time.Time
	inflight chan struct{} // 非 nil 表示正在探测，探测结束时关闭
}

func newInventoryCache(probe func(context.Context) sandbox.Inventory) *inventoryCache {
	return &inventoryCache{probe: probe}
}

// get 返回安装状态。从未探测过时等待首次探测结束；ctx 先结束则返回它的错误。
func (c *inventoryCache) get(ctx context.Context) (sandbox.Inventory, error) {
	c.mu.Lock()
	if c.probed && time.Now().Before(c.expires) {
		v := c.value
		c.mu.Unlock()
		return v, nil
	}
	done := c.refreshLocked()
	if c.probed {
		v := c.value
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	select {
	case <-done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.value, nil
	case <-ctx.Done():
		return sandbox.Inventory{}, ctx.Err()
	}
}

// refreshLocked 启动一次探测（已在探测时复用它），返回它结束时关闭的 channel。调用方持有 mu。
func (c *inventoryCache) refreshLocked() <-chan struct{} {
	if c.inflight != nil {
		return c.inflight
	}
	done := make(chan struct{})
	c.inflight = done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), inventoryProbeTimeout)
		defer cancel()
		v := c.probe(ctx)
		c.mu.Lock()
		c.value, c.probed, c.expires, c.inflight = v, true, time.Now().Add(inventoryTTL), nil
		c.mu.Unlock()
		close(done)
	}()
	return done
}

// probeInventory 探测工具清单中每个 CLI 工具能否被命令调用，以及浏览器能力。browser-use 命令
// 由沙盒自己提供（见 browser.Install），它可用与否就是浏览器能力。
func (s *Server) probeInventory(ctx context.Context) sandbox.Inventory {
	inv := sandbox.Inventory{Tools: make(map[string]bool, len(s.cfg.Tools))}
	if s.cfg.BrowserPython == "" {
		inv.Browser = sandbox.BrowserStatus{Detail: "未配置 sandbox.browser_python"}
	} else {
		st := browser.Probe(ctx, s.cfg.BrowserPython)
		inv.Browser = sandbox.BrowserStatus{Installed: st.Installed, Detail: st.Detail}
	}
	var names []string
	for _, name := range s.cfg.Tools {
		if name == browser.Command {
			inv.Tools[name] = inv.Browser.Installed
			continue
		}
		inv.Tools[name] = false
		names = append(names, name)
	}
	found, err := s.findCommands(ctx, names)
	if err != nil {
		inventoryLog.Warn().Err(err).Msg("探测 CLI 工具失败，按全部未安装上报")
	}
	for _, name := range found {
		if _, listed := inv.Tools[name]; listed {
			inv.Tools[name] = true
		}
	}
	return inv
}

// findCommands 返回 names 中命令能调用到的那些：在执行命令的同一 shell、同一命令环境里
// 逐个 command -v。命令看到的 PATH（含沙盒前置的浏览器脚本目录）与可执行判定（Windows 上
// 无扩展名的脚本、.exe）都以 shell 为准，探测与实际执行不会各说各话。探测脚本经标准输入
// 交给 shell，不经命令行。
func (s *Server) findCommands(ctx context.Context, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	var script strings.Builder
	for _, name := range names {
		q := shellQuote(name)
		fmt.Fprintf(&script, "command -v %s >/dev/null 2>&1 && printf '%%s\\n' %s\n", q, q)
	}
	script.WriteString("exit 0\n")
	cmd := exec.CommandContext(ctx, s.cfg.Shell, "-s") // #nosec G204 G702 // shell 来自部署配置，探测脚本只含工具清单里的名称
	cmd.Env = s.baseEnv()
	cmd.Stdin = strings.NewReader(script.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", s.cfg.Shell, err, strings.TrimSpace(stderr.String()))
	}
	return strings.Fields(string(out)), nil
}

// shellQuote 用单引号包裹 s，供 sh 原样解析。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
