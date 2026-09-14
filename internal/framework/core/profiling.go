package core

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"runtime/pprof"
	"time"
)

// ProfilerConfig 是性能剖析配置。
type ProfilerConfig struct {
	// Enabled 是否启用性能剖析
	Enabled bool

	// CPUProfileRate 设置 CPU 采样率（Hz），0 表示使用默认值 100Hz
	CPUProfileRate int

	// MemProfileRate 设置内存采样率（字节），0 表示使用默认值
	MemProfileRate int

	// MutexProfileFraction 设置互斥锁竞争采样率，0 表示禁用
	MutexProfileFraction int

	// BlockProfileRate 设置阻塞事件采样率（纳秒），0 表示禁用
	BlockProfileRate int
}

// DefaultProfilerConfig 返回默认配置。
func DefaultProfilerConfig() ProfilerConfig {
	return ProfilerConfig{
		Enabled:              true,
		CPUProfileRate:       100,
		MemProfileRate:       512 * 1024,  // 512KB
		MutexProfileFraction: 1,            // 采样所有事件
		BlockProfileRate:     1,            // 采样所有阻塞
	}
}

// Profiler 是性能剖析器，集成 Go pprof。
type Profiler struct {
	config ProfilerConfig
}

// NewProfiler 创建性能剖析器。
func NewProfiler(config ProfilerConfig) *Profiler {
	p := &Profiler{
		config: config,
	}

	if config.Enabled {
		p.configure()
	}

	return p
}

// configure 配置 runtime profiling。
func (p *Profiler) configure() {
	if p.config.CPUProfileRate > 0 {
		runtime.SetCPUProfileRate(p.config.CPUProfileRate)
	}

	if p.config.MemProfileRate > 0 {
		runtime.MemProfileRate = p.config.MemProfileRate
	}

	if p.config.MutexProfileFraction > 0 {
		runtime.SetMutexProfileFraction(p.config.MutexProfileFraction)
	}

	if p.config.BlockProfileRate > 0 {
		runtime.SetBlockProfileRate(p.config.BlockProfileRate)
	}
}

// Handler 返回 pprof HTTP 处理器。
// 挂载到 /debug/pprof/ 路径下，提供：
//   - /debug/pprof/profile - CPU profile（30秒采样）
//   - /debug/pprof/heap - 堆内存 profile
//   - /debug/pprof/goroutine - goroutine profile
//   - /debug/pprof/block - 阻塞 profile
//   - /debug/pprof/mutex - 互斥锁 profile
//   - /debug/pprof/allocs - 内存分配 profile
//   - /debug/pprof/threadcreate - 线程创建 profile
func (p *Profiler) Handler() http.Handler {
	mux := http.NewServeMux()

	// 索引页
	mux.HandleFunc("/debug/pprof/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/debug/pprof/" {
			p.indexHandler(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// 各类 profile
	mux.HandleFunc("/debug/pprof/profile", p.profileHandler)
	mux.HandleFunc("/debug/pprof/heap", p.heapHandler)
	mux.HandleFunc("/debug/pprof/goroutine", p.goroutineHandler)
	mux.HandleFunc("/debug/pprof/block", p.blockHandler)
	mux.HandleFunc("/debug/pprof/mutex", p.mutexHandler)
	mux.HandleFunc("/debug/pprof/allocs", p.allocsHandler)
	mux.HandleFunc("/debug/pprof/threadcreate", p.threadcreateHandler)

	// cmdline 和 symbol（用于分析工具）
	mux.HandleFunc("/debug/pprof/cmdline", p.cmdlineHandler)
	mux.HandleFunc("/debug/pprof/symbol", p.symbolHandler)

	return mux
}

// indexHandler 处理索引页。
func (p *Profiler) indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
<title>pprof 性能剖析</title>
<style>
body { font-family: sans-serif; margin: 40px; }
h1 { color: #333; }
ul { line-height: 1.8; }
a { color: #0066cc; text-decoration: none; }
a:hover { text-decoration: underline; }
.note { color: #666; font-size: 0.9em; margin-top: 20px; }
</style>
</head>
<body>
<h1>pprof 性能剖析</h1>
<p>可用的 profiles：</p>
<ul>
<li><a href="/debug/pprof/profile?seconds=30">CPU Profile</a> - CPU 使用情况（30秒采样）</li>
<li><a href="/debug/pprof/heap">Heap Profile</a> - 堆内存使用情况</li>
<li><a href="/debug/pprof/goroutine">Goroutine Profile</a> - 所有 goroutine 的堆栈</li>
<li><a href="/debug/pprof/block">Block Profile</a> - 阻塞操作统计</li>
<li><a href="/debug/pprof/mutex">Mutex Profile</a> - 互斥锁竞争统计</li>
<li><a href="/debug/pprof/allocs">Allocs Profile</a> - 内存分配统计</li>
<li><a href="/debug/pprof/threadcreate">Thread Create Profile</a> - 线程创建统计</li>
</ul>
<p class="note">
使用 <code>go tool pprof</code> 分析：<br>
<code>go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30</code>
</p>
</body>
</html>`)
}

// profileHandler 处理 CPU profile。
func (p *Profiler) profileHandler(w http.ResponseWriter, r *http.Request) {
	seconds := 30
	if sec := r.URL.Query().Get("seconds"); sec != "" {
		if s, err := time.ParseDuration(sec + "s"); err == nil {
			seconds = int(s.Seconds())
		}
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=profile")

	if err := pprof.StartCPUProfile(w); err != nil {
		http.Error(w, fmt.Sprintf("无法启动 CPU profile: %v", err), http.StatusInternalServerError)
		return
	}

	time.Sleep(time.Duration(seconds) * time.Second)
	pprof.StopCPUProfile()
}

// heapHandler 处理堆内存 profile。
func (p *Profiler) heapHandler(w http.ResponseWriter, r *http.Request) {
	runtime.GC() // 触发 GC 以获得更准确的结果
	p.writeProfile(w, "heap")
}

// goroutineHandler 处理 goroutine profile。
func (p *Profiler) goroutineHandler(w http.ResponseWriter, r *http.Request) {
	p.writeProfile(w, "goroutine")
}

// blockHandler 处理阻塞 profile。
func (p *Profiler) blockHandler(w http.ResponseWriter, r *http.Request) {
	p.writeProfile(w, "block")
}

// mutexHandler 处理互斥锁 profile。
func (p *Profiler) mutexHandler(w http.ResponseWriter, r *http.Request) {
	p.writeProfile(w, "mutex")
}

// allocsHandler 处理内存分配 profile。
func (p *Profiler) allocsHandler(w http.ResponseWriter, r *http.Request) {
	runtime.GC()
	p.writeProfile(w, "allocs")
}

// threadcreateHandler 处理线程创建 profile。
func (p *Profiler) threadcreateHandler(w http.ResponseWriter, r *http.Request) {
	p.writeProfile(w, "threadcreate")
}

// cmdlineHandler 处理 cmdline 请求。
func (p *Profiler) cmdlineHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "liusha-adk\n")
}

// symbolHandler 处理 symbol 请求。
func (p *Profiler) symbolHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// symbol 解析由 go tool pprof 处理
	fmt.Fprintf(w, "num_symbols: 0\n")
}

// writeProfile 写入指定类型的 profile。
func (p *Profiler) writeProfile(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", name))

	profile := pprof.Lookup(name)
	if profile == nil {
		http.Error(w, fmt.Sprintf("未知的 profile: %s", name), http.StatusNotFound)
		return
	}

	if err := profile.WriteTo(w, 0); err != nil {
		http.Error(w, fmt.Sprintf("无法写入 profile: %v", err), http.StatusInternalServerError)
		return
	}
}

// ─────────────────────────────────────────────
//  性能快照
// ─────────────────────────────────────────────

// RuntimeStats 是运行时统计信息。
type RuntimeStats struct {
	// Goroutine 数量
	NumGoroutine int

	// 内存统计
	Alloc      uint64 // 已分配且仍在使用的字节数
	TotalAlloc uint64 // 累计分配的字节数
	Sys        uint64 // 从系统获得的字节数
	NumGC      uint32 // GC 运行次数

	// GC 统计
	PauseTotalNs uint64 // GC 暂停总时间（纳秒）
	LastGC       uint64 // 上次 GC 的时间戳（纳秒）
}

// GetRuntimeStats 获取当前运行时统计信息。
func (p *Profiler) GetRuntimeStats() RuntimeStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return RuntimeStats{
		NumGoroutine: runtime.NumGoroutine(),
		Alloc:        m.Alloc,
		TotalAlloc:   m.TotalAlloc,
		Sys:          m.Sys,
		NumGC:        m.NumGC,
		PauseTotalNs: m.PauseTotalNs,
		LastGC:       m.LastGC,
	}
}

// ─────────────────────────────────────────────
//  Callback 集成
// ─────────────────────────────────────────────

// ProfilingCallback 是性能剖析 Callback 实现。
type ProfilingCallback struct {
	NoopCallback
	profiler *Profiler
}

// NewProfilingCallback 创建性能剖析 Callback。
func NewProfilingCallback(profiler *Profiler) *ProfilingCallback {
	return &ProfilingCallback{
		profiler: profiler,
	}
}

// OnAgentStart Agent 启动时可以记录快照。
func (c *ProfilingCallback) OnAgentStart(ctx context.Context, event AgentStartEvent) {
	// 可选：记录启动时的运行时状态
	// stats := c.profiler.GetRuntimeStats()
	// log with stats
}

// OnAgentEnd Agent 结束时可以记录快照。
func (c *ProfilingCallback) OnAgentEnd(ctx context.Context, event AgentEndEvent) {
	// 可选：记录结束时的运行时状态
	// stats := c.profiler.GetRuntimeStats()
	// log with stats
}
