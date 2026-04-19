// Package proxy 封装 Proxify 子进程，并将其 JSONL 输出转换为 flow.Flow 写入持久化层。
package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/V3teran/liusha/internal/logx"
	"github.com/google/uuid"
)

// ParsedFlow 是从 proxify JSONL 解析出的单条流量，尚未经 ScopePolicy 过滤。
type ParsedFlow struct {
	EngagementID uuid.UUID
	Timestamp    time.Time
	Method       string
	URL          string
	ReqHeaders   map[string]string
	ReqBody      []byte
	RespStatus   int
	RespHeaders  map[string]string
	RespBody     []byte
}

// proxifyRecord 对应 proxify -of jsonl 的输出格式。
type proxifyRecord struct {
	Timestamp string `json:"timestamp"`
	URL       string `json:"url"`
	Request   struct {
		Method string            `json:"method"`
		Header map[string]string `json:"header"`
		Body   string            `json:"body"` // base64 编码或空字符串
	} `json:"request"`
	Response struct {
		StatusCode int               `json:"status_code"`
		Header     map[string]string `json:"header"`
		Body       string            `json:"body"`
	} `json:"response"`
}

// TailJSONL 持续 tail path 文件，将解析成功的行推入 ch；ctx 取消时退出。
// 文件不存在时每 500ms 轮询等待（proxify 启动需要时间才生成文件）。
func TailJSONL(ctx context.Context, path string, engID uuid.UUID, ch chan<- ParsedFlow) {
	bootCtx := logx.WithTraceID(ctx, logx.NewTraceID())
	var (
		file   *os.File
		offset int64
	)

	for {
		if file == nil {
			f, err := os.Open(path)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(500 * time.Millisecond):
					continue
				}
			}
			file = f
			if _, err := file.Seek(offset, io.SeekStart); err != nil {
				logx.From(bootCtx).Warn().Err(err).Msg("seek 失败，从头读取")
				offset = 0
			}
		}

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			pf, err := parseProxifyLine(line, engID)
			if err != nil {
				logx.From(bootCtx).Warn().Err(err).Msg("解析 proxify JSONL 行失败，跳过")
				continue
			}
			select {
			case ch <- pf:
			case <-ctx.Done():
				return
			}
		}
		// 到达文件末尾，记录当前偏移，等待新内容写入
		cur, _ := file.Seek(0, io.SeekCurrent)
		offset = cur

		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func parseProxifyLine(line []byte, engID uuid.UUID) (ParsedFlow, error) {
	var rec proxifyRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return ParsedFlow{}, err
	}
	ts, err := time.Parse(time.RFC3339, rec.Timestamp)
	if err != nil {
		ts = time.Now().UTC()
	}
	return ParsedFlow{
		EngagementID: engID,
		Timestamp:    ts,
		Method:       rec.Request.Method,
		URL:          rec.URL,
		ReqHeaders:   rec.Request.Header,
		ReqBody:      decodeBody(rec.Request.Body),
		RespStatus:   rec.Response.StatusCode,
		RespHeaders:  rec.Response.Header,
		RespBody:     decodeBody(rec.Response.Body),
	}, nil
}

// decodeBody 尝试 base64 StdEncoding 解码；失败则视为明文 UTF-8。
func decodeBody(s string) []byte {
	if s == "" {
		return nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b
	}
	return []byte(s)
}
