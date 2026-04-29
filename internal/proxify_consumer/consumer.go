// Package proxify_consumer 实现：tail proxify 输出的 JSONL → 解析 → 入 flow / window 库 →
// 关窗时把 sniffer 任务投到 asynq 队列。是 BAC pipeline 的入口。
package proxify_consumer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/window"
	"github.com/V3teran/liusha/internal/worker"
)

// proxifyRecord 是 proxify 默认 JSONL schema 的最小子集。
// 字段顺序与 plan 给的样例对齐：request.method / url / headers / body；response.status_code / headers / body。
// headers 取多值（map[string][]string），落库时由 flattenHeaders 折成单值。
type proxifyRecord struct {
	Request struct {
		Method  string              `json:"method"`
		URL     string              `json:"url"`
		Headers map[string][]string `json:"headers"`
		Body    string              `json:"body"`
	} `json:"request"`
	Response struct {
		StatusCode int                 `json:"status_code"`
		Headers    map[string][]string `json:"headers"`
		Body       string              `json:"body"`
	} `json:"response"`
}

// parseLine 把单行 JSONL 反序列化为 proxifyRecord；非 JSON 返回 error。
func parseLine(line []byte) (proxifyRecord, error) {
	var r proxifyRecord
	if err := json.Unmarshal(line, &r); err != nil {
		return proxifyRecord{}, fmt.Errorf("parse proxify line: %w", err)
	}
	return r, nil
}

// hostFromURL 从绝对 URL 提取主机名（去掉 port），用作 engagement.scope_host。
// 缺 host 时返回 error，避免落库一行 host="" 的脏数据。
func hostFromURL(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse url %q: %w", s, err)
	}
	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("url %q missing host", s)
	}
	return host, nil
}

// flattenHeaders 把多值 header（HTTP 协议层）折成单字符串（DB 落库层），多值用 ", " 拼接。
// 空 map 返回非 nil 空 map（避免下游解引用问题）。
func flattenHeaders(in map[string][]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, vs := range in {
		out[k] = strings.Join(vs, ", ")
	}
	return out
}

// allowHost 判断 host 是否在白名单。空/ nil 白名单视为放行所有（v1 默认）。
func allowHost(host string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, h := range allow {
		if h == host {
			return true
		}
	}
	return false
}

// 默认 tenant：v1 单租户，写死 "default"。
const defaultTenant = "default"

// 文件不存在时的 poll 间隔。
const filePollInterval = time.Second

// Consumer 是 proxify_consumer 的执行器：tail JSONL → 解析 → flow/window 入库 → enqueue sniffer。
type Consumer struct {
	jsonlPath string
	engs      *engagement.Store
	flows     *flow.Store
	windows   *window.Store
	enq       *worker.Client
	cfg       Config
	logger    zerolog.Logger
}

// Config 是 Consumer 运行参数（来自 config.ProxyConfig 的子集 + sweeper 间隔）。
type Config struct {
	WindowBatch         int      // 窗口攒多少条触发 close
	WindowMaxAgeSeconds int      // 窗口存活上限（兜底）
	AllowHosts          []string // 允许采集的 host 白名单；空 = 放行所有
}

// NewConsumer 构造 Consumer。所有 store 依赖必填。
func NewConsumer(
	jsonlPath string,
	engs *engagement.Store,
	flows *flow.Store,
	windows *window.Store,
	enq *worker.Client,
	cfg Config,
	logger zerolog.Logger,
) *Consumer {
	return &Consumer{
		jsonlPath: jsonlPath,
		engs:      engs,
		flows:     flows,
		windows:   windows,
		enq:       enq,
		cfg:       cfg,
		logger:    logger,
	}
}

// Run 启动消费主循环：等待文件就绪 → tail → 处理每行 → 周期性兜底关窗。
// ctx 取消时优雅退出。
func (c *Consumer) Run(ctx context.Context) error {
	f, err := c.openWhenReady(ctx)
	if err != nil {
		return err
	}
	defer f.Close()

	// seek 到文件末尾：避免重启后重新消费历史行（asynq 幂等也兜不住几十万行重放）。
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek end: %w", err)
	}

	// 启动周期性 sweeper：兜底关闭超龄 open 窗口。
	sweeperStop := c.startSweeper(ctx)
	defer sweeperStop()

	reader := bufio.NewReader(f)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			c.handleLine(ctx, line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				// tail：未读到完整行，等一会儿再读。
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(200 * time.Millisecond):
				}
				continue
			}
			return fmt.Errorf("read jsonl: %w", err)
		}
	}
}

// openWhenReady 在文件未生成时 poll 等待；ctx 取消立即退出。
func (c *Consumer) openWhenReady(ctx context.Context) (*os.File, error) {
	for {
		f, err := os.Open(c.jsonlPath)
		if err == nil {
			return f, nil
		}
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("open %s: %w", c.jsonlPath, err)
		}
		c.logger.Debug().Str("path", c.jsonlPath).Msg("等待 proxify JSONL 文件就绪")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(filePollInterval):
		}
	}
}

// handleLine 处理单行：解析 → 校验 host → 落 engagement/flow/window → 关窗 enqueue。
// 任意错误仅 log warn 并跳过当前行，不 panic 不退出循环。
func (c *Consumer) handleLine(ctx context.Context, raw []byte) {
	line := dropTrailingNewline(raw)
	if len(line) == 0 {
		return
	}
	rec, err := parseLine(line)
	if err != nil {
		c.logger.Warn().Err(err).Bytes("raw", trim(line, 256)).Msg("proxify 行解析失败，跳过")
		return
	}

	host, err := hostFromURL(rec.Request.URL)
	if err != nil {
		c.logger.Warn().Err(err).Str("url", rec.Request.URL).Msg("提取 host 失败，跳过")
		return
	}
	if !allowHost(host, c.cfg.AllowHosts) {
		c.logger.Debug().Str("host", host).Msg("host 不在白名单，跳过")
		return
	}

	eng, err := c.engs.LookupOrCreate(ctx, defaultTenant, host, engagement.ModeProxy)
	if err != nil {
		c.logger.Warn().Err(err).Str("host", host).Msg("engagement LookupOrCreate 失败")
		return
	}

	flowID, err := c.appendFlow(ctx, eng.ID, rec)
	if err != nil {
		c.logger.Warn().Err(err).Str("eid", eng.ID).Msg("flow 入库失败")
		return
	}

	wid, justClosed, err := c.windows.OpenOrAppend(ctx, eng.ID, window.FlowRef{
		ID:     flowID,
		Method: rec.Request.Method,
		URL:    rec.Request.URL,
	}, c.cfg.WindowBatch)
	if err != nil {
		c.logger.Warn().Err(err).Str("eid", eng.ID).Msg("window OpenOrAppend 失败")
		return
	}
	if justClosed {
		c.enqueueSniffer(ctx, eng.ID, wid)
	}
}

// appendFlow 把 proxifyRecord 转成 flow.Flow 并写库，返回新行 id。
func (c *Consumer) appendFlow(ctx context.Context, eid string, rec proxifyRecord) (int64, error) {
	reqH, err := json.Marshal(flattenHeaders(rec.Request.Headers))
	if err != nil {
		return 0, fmt.Errorf("marshal request headers: %w", err)
	}
	respH, err := json.Marshal(flattenHeaders(rec.Response.Headers))
	if err != nil {
		return 0, fmt.Errorf("marshal response headers: %w", err)
	}
	return c.flows.Append(ctx, flow.Flow{
		EngagementID:    eid,
		Method:          rec.Request.Method,
		URL:             rec.Request.URL,
		RequestHeaders:  reqH,
		RequestBody:     []byte(rec.Request.Body),
		StatusCode:      rec.Response.StatusCode,
		ResponseHeaders: respH,
		ResponseBody:    []byte(rec.Response.Body),
	})
}

// enqueueSniffer 把刚关闭的窗口投递为 sniffer 任务（spec §3.2 主链路入口）。
func (c *Consumer) enqueueSniffer(ctx context.Context, eid, windowID string) {
	taskID := uuid.NewString()
	input, err := json.Marshal(map[string]string{"window_id": windowID})
	if err != nil {
		c.logger.Warn().Err(err).Msg("序列化 sniffer input 失败")
		return
	}
	_, _, err = c.enq.Enqueue(ctx, worker.RoleSniffer, worker.Payload{
		TaskID:       taskID,
		EngagementID: eid,
		Input:        input,
	})
	if err != nil {
		c.logger.Warn().Err(err).Str("eid", eid).Str("window_id", windowID).Msg("sniffer 入队失败")
		return
	}
	c.logger.Info().Str("eid", eid).Str("window_id", windowID).Str("task_id", taskID).
		Msg("窗口关闭，已投递 sniffer 任务")
}

// startSweeper 周期性兜底关闭超龄 open 窗口。返回 stop func；ctx 取消会自动停止。
func (c *Consumer) startSweeper(ctx context.Context) func() {
	if c.cfg.WindowMaxAgeSeconds <= 0 {
		return func() {}
	}
	interval := time.Duration(c.cfg.WindowMaxAgeSeconds) * time.Second
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-t.C:
				// v1 没有 engagement 列表枚举接口；sweeper 需要 caller 在 windowing 层做兜底。
				// 当前实现：在 store 里 CloseExpired 是按 engagementID 关，全局扫待 T7 加 ListActive。
				// TODO(T7): 拿 active engagement 列表后调用 c.windows.CloseExpired(ctx, eid, maxAge)。
				c.logger.Debug().Msg("sweeper tick（待 T7 接 active engagement 列表）")
			}
		}
	}()
	return func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
	}
}

// dropTrailingNewline 去掉行尾 \n / \r\n（bufio.ReadBytes 会保留分隔符）。
func dropTrailingNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// trim 限制 log 时打印的字节数，避免一行十几 KiB 撑爆日志。
func trim(b []byte, max int) []byte {
	if len(b) <= max {
		return b
	}
	return b[:max]
}
