// Package middleware 提供 ReAct Action 横切层。
//
// 设计要点：工具结果压缩——大输出存盘 + tail 摘要回传，控 LLM 上下文体积。
package middleware

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/toolruntime"
)

// fallback 阈值：caller 传 0 时使用。
const (
	// fallbackThreshold = 64 KB：与 sandbox.run_tail_bytes×2 留余量。
	fallbackThreshold = 64 * 1024
	// fallbackSnippet = 16 KB：截断后喂 LLM 的概览大小（足够看清完整 sqlmap stdout）。
	fallbackSnippet = 16 * 1024
	// fallbackSummary = 1 KB：Result.Summary 长度（喂 Reviewer 滑动窗用）。
	fallbackSummary = 1024
)

// ResultCompress 工厂：返回一个把超阈值 Output 截断的 Middleware。
//
// 当 Output > threshold 时：
//   - Output 字段被截断为前 snippet 字节（直接喂回 LLM 的内容）
//   - Summary 字段被设为前 summary 字节（喂 Reviewer 滑动窗的概要）
//
// threshold/snippet/summary 任一 ≤0 时使用上方 fallback 常量。
//
// 设计动机：LLM context 累积——每次工具调用结果都留在 prompt 直到 task 结束。
// 单工具输出超过 64KB 时质量下降（注意力被稀释），所以截断到 snippet 大小反而利于决策。
// 不落盘——之前的 engagement-store/*.txt 文件无下游消费（无工具能读它），价值仅在调试。
func ResultCompress(threshold, snippet, summary int) toolfx.Middleware {
	if threshold <= 0 {
		threshold = fallbackThreshold
	}
	if snippet <= 0 {
		snippet = fallbackSnippet
	}
	if summary <= 0 {
		summary = fallbackSummary
	}

	return func(next toolfx.ActionExecutor) toolfx.ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (toolfx.Result, error) {
			res, err := next(ctx, name, args)
			if err != nil {
				return res, err
			}
			if len(res.Output) <= threshold {
				return res, nil
			}

			truncated := res.Output[:snippet]
			summaryText := string(truncated)
			if len(summaryText) > summary {
				summaryText = summaryText[:summary]
			}
			return toolfx.Result{
				Output:  truncated,
				Summary: summaryText,
				Done:    res.Done,
			}, nil
		}
	}
}
