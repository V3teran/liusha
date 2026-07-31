package einotools

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// insight.go：eino 版 mark_insight 工具——让 agent 主动标记「关键判断 / 关键发现」。
//
// 这是执行图第二趟语义提炼的最可靠来源（§2 self-mark，Provenance=agent）：与其让 LLM 事后
// 从上千条 reasoning 里猜哪句是关键，不如让 agent 在当下自己说「这是一个我要验证的判断」
// 或「这是一个关键观察」。无副作用、不落任何业务表——纯语义信号，由 WrapToolCall middleware
// 捕获成 ScanEventInsight 事件（同 task→spawn 的处理方式），投影时提炼成 hypothesis / signal 节点。
//
// 与 write_lead 的边界：write_lead 是「跨 agent 共享的情报」（改变别的 agent 行为）；
// mark_insight 是「标记本条调查线的关键节点」（只影响执行图可读性，不进黑板、不跨 agent）。

// InsightType 是 mark_insight 的 type 取值（对齐 attackgraph 的 hypothesis / signal 语义）。
const (
	InsightHypothesis = "hypothesis" // 判断：我认为/怀疑 X（待验证的猜想）
	InsightSignal     = "signal"     // 信号：我观察到/发现 X（关键线索或死路）
)

// markInsightArgs 是 mark_insight 入参。text 必填；type 二选一；dead_end 仅对 signal 有意义。
type markInsightArgs struct {
	Type    string `json:"type" jsonschema:"required,enum=hypothesis,enum=signal" jsonschema_description:"hypothesis=一个待验证的判断/猜想；signal=一个关键观察/线索/死路"`
	Text    string `json:"text" jsonschema:"required" jsonschema_description:"一句人话说清这个判断或发现（≤120 字），执行图里直接当节点标题显示"`
	DeadEnd bool   `json:"dead_end,omitempty" jsonschema_description:"仅 signal 用：此路不通/已确认没戏时置 true，图中标记为死路，提醒别再试"`
}

// BuildMarkInsight 造 eino 版 mark_insight 工具。无身份注入、无存储副作用——
// 校验入参即返回 ok，真正的「成图」由 einoagent 的 WrapToolCall middleware 发事件驱动。
func BuildMarkInsight() (tool.BaseTool, error) {
	return utils.InferTool(
		"mark_insight",
		"在执行图上标记一个关键节点，帮观察者一眼看懂你的调查思路。在两种时机调用：\n"+
			"- 形成一个要验证的判断时（type=hypothesis），如『怀疑 /api/user 存在 IDOR，越权可读他人数据』\n"+
			"- 得到一个关键观察时（type=signal），如『响应头暴露 X-Powered-By: PHP/5.6，版本已知多个 RCE』；"+
			"若确认此路不通，置 dead_end=true（如『所有上传绕过 payload 均被 WAF 拦，此路不通』）\n\n"+
			"只标真正推动/转折调查的节点，别把每步心算都标（那些留在 reasoning 里即可）。"+
			"本工具不改任何状态、不跨 agent 共享，只让执行图更可读。",
		func(_ context.Context, in markInsightArgs) (map[string]any, error) {
			switch in.Type {
			case InsightHypothesis, InsightSignal:
			default:
				return nil, fmt.Errorf("type 取值非法 %q（仅支持 hypothesis | signal）", in.Type)
			}
			if in.Text == "" {
				return nil, errors.New("text 必填")
			}
			return map[string]any{"ok": true}, nil
		})
}
