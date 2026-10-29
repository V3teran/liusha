// 日志查看 handler（前端 /logs 页）。
//
// 数据面零新依赖：logx 写入端把每进程 JSON lines 落到共享日志卷的
// logs/{process}.log，本 handler 经 *logx.Reader 尾读这些文件——文件与 UI
// 是同一事实源，不引入任何日志传输/存储中间层。
//
// GET /logs/processes：进程文件概况（存在性/大小/近期实例），筛选下拉用。
// GET /logs：尾读 + 游标增量轮询（cursor=name:offset 逗号串）。

package httpapi

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/logx"
)

// LogsAPI 是日志查看窄接口；*logx.Reader 自动满足。
type LogsAPI interface {
	LogProcesses() []logx.ProcessInfo
	ReadLogs(q logx.ReadQuery) (logx.ReadResult, error)
}

// 尾读条数默认值/上限：一屏热窗口的量级，冷留存归文件轮转管。
const (
	defaultLogLimit = 500
	maxLogLimit     = 1000
)

var logLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// parseLogQuery 从 query 解析筛选与游标。level/process/cursor 语法错误显式 400
// （契约错误），坏值静默放行会变成「查不到数据」的假象。
func parseLogQuery(c *gin.Context) (logx.ReadQuery, error) {
	q := logx.ReadQuery{
		Level:    strings.TrimSpace(c.Query("level")),
		Instance: strings.TrimSpace(c.Query("instance")),
		Search:   strings.TrimSpace(c.Query("search")),
		Limit:    atoiOr(c.Query("limit"), defaultLogLimit),
	}
	if q.Limit < 1 {
		q.Limit = defaultLogLimit
	}
	if q.Limit > maxLogLimit {
		q.Limit = maxLogLimit
	}
	if q.Level != "" && !logLevels[q.Level] {
		return q, fmt.Errorf("非法 level %q（可选 debug/info/warn/error）", q.Level)
	}

	switch p := strings.TrimSpace(c.Query("process")); p {
	case "", "all":
	default:
		if !slices.Contains(logx.ViewProcesses, p) {
			return q, fmt.Errorf("非法 process %q（可选 %s/all）", p, strings.Join(logx.ViewProcesses, "/"))
		}
		q.Processes = []string{p}
	}

	if cs := strings.TrimSpace(c.Query("cursor")); cs != "" {
		cursors, err := parseLogCursor(cs)
		if err != nil {
			return q, err
		}
		q.Cursors = cursors
	}
	return q, nil
}

// parseLogCursor 解析 name:offset 逗号串。视图域之外的进程名忽略（客户端从
// 全进程视图切到单进程视图时带着多余游标是正常流程），语法/数值错误才报错。
func parseLogCursor(s string) (map[string]int64, error) {
	out := make(map[string]int64)
	for _, part := range strings.Split(s, ",") {
		name, off, found := strings.Cut(strings.TrimSpace(part), ":")
		if !found || name == "" {
			return nil, fmt.Errorf("非法 cursor 段 %q（应为 name:offset）", part)
		}
		v, err := strconv.ParseInt(off, 10, 64)
		if err != nil || v < 0 {
			return nil, fmt.Errorf("非法 cursor 偏移 %q", part)
		}
		if slices.Contains(logx.ViewProcesses, name) {
			out[name] = v
		}
	}
	return out, nil
}

// logProcessesHandler 处理 GET /logs/processes。
func logProcessesHandler(api LogsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"processes": api.LogProcesses()})
	}
}

// readLogsHandler 处理 GET /logs?process=&level=&instance=&search=&limit=&cursor=。
//
// 返回：
//
//	{ entries: [...按时间升序], cursors: {进程: 续读偏移}, reset: [...], truncated: [...] }
func readLogsHandler(api LogsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		q, err := parseLogQuery(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		res, err := api.ReadLogs(q)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"entries":   res.Entries,
			"cursors":   res.Cursors,
			"reset":     res.Reset,
			"truncated": res.Truncated,
		})
	}
}
