package einotools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// done.go：eino 版终止工具，工具名 **done**（对齐 liusha hunter prompt —— system_prompt_{shared,exploitation,orchestrator}.md
// 都教 LLM 调 done 收尾）。eino 自带 adk.ExitTool 名为 "exit"，与 prompt 不符 → LLM 调 done 会
// 「tool done not found」报错（active e2e 实测 exploitation 因此挂）。本工具复刻 eino 的 Exit 终止机制
// （SendToolGenAction + NewExitAction），但用 name=done + liusha 熟悉的 reason/summary 参数。
//
// eino 单 agent 本可「不调工具即自然收尾」，但 liusha prompt 是 react/eino 共享资产、深度依赖 done，
// 故注册真 done 工具比改 prompt 更稳（prompt 不动、react 不受影响）。

// doneTool 实现 tool.InvokableTool，InvokableRun 触发 eino agent 终止。
type doneTool struct{}

func (doneTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "done",
		Desc: "终止当前任务收尾。可带 reason / summary（供 Inspector / 报告参考）。" +
			"完成深挖 / 确认无漏洞 / 主向量验完即调本工具结束。",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"reason":  {Type: schema.String, Desc: "收尾原因（可选）"},
			"summary": {Type: schema.String, Desc: "任务总结（可选）"},
		}),
	}, nil
}

// InvokableRun 发 eino Exit action 终止 agent，返回最终结果。
func (doneTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	if err := adk.SendToolGenAction(ctx, "done", adk.NewExitAction()); err != nil {
		return "", fmt.Errorf("done: 发送终止 action 失败: %w", err)
	}
	return "task done", nil
}

// BuildDone 造 eino 版 done 终止工具（三角色共用）。
func BuildDone() (tool.BaseTool, error) {
	return doneTool{}, nil
}
