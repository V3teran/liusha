package einotools

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/V3teran/liusha/internal/lead"
)

// LeadAdder 是窄接口，*lead.Store 自动满足。
type LeadAdder interface {
	Append(ctx context.Context, host string, e lead.Entry) error
}

// writeLeadArgs 是 write_lead 入参；kind/note 均必填，身份值（host/hunter_id/source_task_id）闭包注入。
type writeLeadArgs struct {
	Kind string `json:"kind" jsonschema:"required,enum=clue,enum=fact,enum=deadend" jsonschema_description:"clue=可疑点(待验证)；fact=既成发现(记住并利用)；deadend=死路(绕开别试)"`
	Note string `json:"note" jsonschema:"required" jsonschema_description:"一句人话，位置/细节都在这里说清（≤200 字）"`
}

// BuildWriteLead 造原生 eino write_lead 工具。host/hunterID/sourceTaskID 闭包捕获，不进 LLM 参数
// （防串库，同 write_finding/write_lesson 语义）。授予 recon/exploitation/traffic-analysis 三角色。
func BuildWriteLead(store LeadAdder, host, hunterID, sourceTaskID string) (tool.BaseTool, error) {
	return utils.InferTool(
		"write_lead",
		"写一条「跨 agent 情报」到情报黑板（按 host 共享，子代理/跨 run 都能看到；不进交付报告）。"+
			"\n\nkind 三选一，按你接下来打算怎么对待这条情报选："+
			"\n- clue：可疑点，还没验证，下一步该去试试（如『/admin/backup 目录疑似可访问，未验证』）"+
			"\n- fact：已确认的事实，但还没做成可复现 PoC（如『该 host 的 session cookie 不含 HttpOnly』）"+
			"\n- deadend：试过没戏的死路，提醒后来者别浪费时间重试（如『/api/upload 已确认无文件类型校验绕过点，多种 payload 均被拒』）"+
			"\n\n【与 write_finding 的边界】能给可复现 PoC + evidence → write_finding；仅观察到事实、还没做成 PoC → write_lead(kind=fact)。"+
			"lead(fact) 验证成 PoC 后应改写 write_finding（lead 不必删，靠淘汰自然消失）。"+
			"\n【禁写】通用 OWASP 理论 / 与本次目标无关的知识 → 不写；一次性无需跨 agent 共享的心算过程 → 直接在对话里说出（reasoning）。",
		func(ctx context.Context, in writeLeadArgs) (map[string]any, error) {
			if host == "" {
				return nil, errors.New("write_lead: Host 注入缺失")
			}
			kind := lead.Kind(in.Kind)
			switch kind {
			case lead.KindClue, lead.KindFact, lead.KindDeadend:
			default:
				return nil, fmt.Errorf("kind 取值非法 %q（仅支持 clue | fact | deadend）", in.Kind)
			}
			if in.Note == "" {
				return nil, errors.New("note 必填")
			}
			if err := store.Append(ctx, host, lead.Entry{
				Kind:         kind,
				Note:         in.Note,
				HunterID:     hunterID,
				SourceTaskID: sourceTaskID,
			}); err != nil {
				return nil, fmt.Errorf("写入情报黑板失败: %w", err)
			}
			return map[string]any{"ok": true}, nil
		})
}
