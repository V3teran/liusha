package einoagent

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// tool_guard.go：工具错误守卫中间件。
//
// 背景：eino deep 把工具返回的 error 升级为致命 NodeRunError，导致**单次**工具调用出错
// （最常见是 LLM 漏填必填参数，如 write_finding 缺 summary）就炸掉**整条** orchestrator run
// ——数分钟的成果与已存 finding 一起丢失（实测一次缺参毁掉 8 分钟扫描）。
//
// 修法（业界 agent tool-use 通行做法）：工具错误不向上抛，而是作为**工具结果**回灌给模型，
// 让它看到错误自我修正重发。仅放行 context 取消/超时（用户中止 / run 超时这种真终止）。
//
// 放置：注册为 mws 最前 = 最外层（chatmodel.go「first registered is outermost」），
// 故工具错误会先经 event_emitter（发带 Err 的 SSE 事件）+ tool_recorder（DB 记 error_message）
// 标记失败，再由本守卫吞掉让 run 继续——观测完整且不崩溃。
func NewToolErrorGuard() adk.AgentMiddleware {
	return adk.AgentMiddleware{
		WrapToolCall: compose.ToolMiddleware{
			// InferTool 系（write_finding / task / read_* 等）走 Invokable。
			Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
				return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
					out, err := next(ctx, in)
					if msg, caught := guardToolErr(in.Name, err); caught {
						return &compose.ToolOutput{Result: msg}, nil
					}
					return out, err
				}
			},
			// run_command / browser_use 走 EnhancedInvokable（多模态结果），错误转文本 part。
			EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
				return func(ctx context.Context, in *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
					out, err := next(ctx, in)
					if msg, caught := guardToolErr(in.Name, err); caught {
						return &compose.EnhancedInvokableToolOutput{Result: &schema.ToolResult{
							Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: msg}},
						}}, nil
					}
					return out, err
				}
			},
		},
	}
}

// guardToolErr 判定工具错误是否应转为结果回灌给模型（caught=true）还是放行向上传播（false）。
// context 取消/超时是真终止（用户中止 / run 超时），放行；其余（含 LLM 参数校验错误）转结果。
func guardToolErr(name string, err error) (string, bool) {
	if err == nil {
		return "", false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "", false
	}
	return "工具 " + name + " 执行失败：" + err.Error() +
		"\n这是单次调用错误，请检查并修正参数后重试，不要因此放弃整个任务。", true
}
