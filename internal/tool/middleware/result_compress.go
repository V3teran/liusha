// Package middleware 提供 ReAct Action 三层横切：result_compress / loop_detect / done_validate。
//
// 设计来源：黑客松借鉴共识 C（Done 系统层裁决）、D（工具结果压缩）、E（代码约束 LoopDetector）。
package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/logx"
)

// resultCompressThreshold 是触发落盘的字节阈值——超过即压缩。
const resultCompressThreshold = 2 * 1024

// snippetSize 是元数据中保留的可读片段长度。
const snippetSize = 400

// summarySize 是写入 Result.Summary 的长度，喂给 Observer 滑动窗。
const summarySize = 200

// ResultCompress 工厂：返回一个把大 Output 落盘 + 替换为元数据 JSON 的 Middleware。
//
// engagementID 用于隔离不同任务的产物目录；baseDir 是落盘根目录（如 engagement-store）。
// 落盘失败时透传原 result + 打印 warn，不阻断 ReAct 主流程。
func ResultCompress(engagementID, baseDir string) tool.Middleware {
	logger := logx.New("action.result_compress")
	var seq atomic.Uint64

	return func(next tool.ActionExecutor) tool.ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (tool.Result, error) {
			res, err := next(ctx, name, args)
			if err != nil {
				return res, err
			}
			if len(res.Output) <= resultCompressThreshold {
				return res, nil
			}

			dir := filepath.Join(baseDir, engagementID)
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				logger.Warn().Err(mkErr).Str("dir", dir).Msg("result_compress 落盘目录创建失败，原样透传")
				return res, nil
			}

			idx := seq.Add(1)
			path := filepath.Join(dir, fmt.Sprintf("%s-%d.txt", name, idx))
			if wErr := os.WriteFile(path, res.Output, 0o644); wErr != nil {
				logger.Warn().Err(wErr).Str("path", path).Msg("result_compress 写文件失败，原样透传")
				return res, nil
			}

			sum := sha256.Sum256(res.Output)
			snippet := makeSnippet(res.Output, snippetSize)

			meta := map[string]any{
				"path":    path,
				"size":    len(res.Output),
				"sha256":  hex.EncodeToString(sum[:]),
				"snippet": snippet,
			}
			encoded, jErr := json.Marshal(meta)
			if jErr != nil {
				logger.Warn().Err(jErr).Msg("result_compress 元数据编码失败，原样透传")
				return res, nil
			}

			summary := snippet
			if len(summary) > summarySize {
				summary = summary[:summarySize]
			}
			return tool.Result{
				Output:  encoded,
				Summary: summary,
				Done:    res.Done,
			}, nil
		}
	}
}

// makeSnippet 截前 n 字节；不足则原样返回。
func makeSnippet(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}
