package einoagent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// tool_recorder.go：eino 路径的 tool_invocation 遥测（gap②，替代 react 的 Record 拦截器）。
//
// 挂 AgentMiddleware.WrapToolCall（compose.ToolMiddleware）：每次工具调用前后落一行 tool_invocation。
// 覆盖 Invokable（InferTool 系）+ EnhancedInvokable（run_command 多模态）两种工具。

const toolOutputPreviewLimit = 4096

// ToolInvocation 是 tool_invocation 落库记录（einoagent 不直接依赖 toolinvocation 包，scanner 适配）。
type ToolInvocation struct {
	HunterID      string
	TaskID        string
	ToolName      string
	Args          json.RawMessage
	OutputSize    int
	OutputPreview string
	DurationMs    int
	ErrorMessage  string
}

// ToolSink 是 tool_invocation 持久化的最小接口（scanner 用 *toolinvocation.Store 适配）。
type ToolSink interface {
	RecordTool(ctx context.Context, inv ToolInvocation)
}

// NewToolRecorder 造记 tool_invocation 的 AgentMiddleware。sink nil 时返回零值 middleware（no-op）。
func NewToolRecorder(sink ToolSink, hunterID, taskID string) adk.AgentMiddleware {
	if sink == nil || hunterID == "" {
		return adk.AgentMiddleware{}
	}
	record := func(name, args, result, errMsg string, dur time.Duration) {
		sink.RecordTool(context.Background(), ToolInvocation{
			HunterID:      hunterID,
			TaskID:        taskID,
			ToolName:      name,
			Args:          rawOrNil(args),
			OutputSize:    len(result),
			OutputPreview: truncate(result, toolOutputPreviewLimit),
			DurationMs:    int(dur.Milliseconds()),
			ErrorMessage:  errMsg,
		})
	}

	return adk.AgentMiddleware{
		WrapToolCall: compose.ToolMiddleware{
			// InferTool 系（read/write_finding、notes、replay_flow…）走 Invokable。
			Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
				return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
					start := time.Now()
					out, err := next(ctx, in)
					result, errMsg := "", ""
					if out != nil {
						result = out.Result
					}
					if err != nil {
						errMsg = err.Error()
					}
					record(in.Name, in.Arguments, result, errMsg, time.Since(start))
					return out, err
				}
			},
			// run_command 走 EnhancedInvokable（多模态 ToolResult），text part 拼成预览。
			EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
				return func(ctx context.Context, in *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
					start := time.Now()
					out, err := next(ctx, in)
					result, errMsg := "", ""
					if out != nil && out.Result != nil {
						result = toolResultText(out.Result)
					}
					if err != nil {
						errMsg = err.Error()
					}
					record(in.Name, in.Arguments, result, errMsg, time.Since(start))
					return out, err
				}
			},
		},
	}
}

// toolResultText 把多模态 ToolResult 的 text part 拼成预览文本（image part 略，只记文本）。
func toolResultText(tr *schema.ToolResult) string {
	var b []byte
	for _, p := range tr.Parts {
		if p.Type == schema.ToolPartTypeText {
			b = append(b, p.Text...)
		}
	}
	return string(b)
}


func rawOrNil(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

func truncate(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	// s[:n] 可能把多字节 UTF-8 字符（中文）从中间切断；工具输出也可能含非 UTF-8 字节（二进制）。
	// PG 强制 UTF-8 会拒收（invalid byte sequence），统一清洗成合法 UTF-8（剔除非法字节）。
	return strings.ToValidUTF8(s, "")
}
