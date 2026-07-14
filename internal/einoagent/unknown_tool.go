package einoagent

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
)

// unknownToolsHandler 兜底模型调用了未注册工具的情形（工具授权清单与 prompt 文案不一致，或模型纯幻觉）。
//
// 不设置 compose.ToolsNodeConfig.UnknownToolsHandler 时，eino ToolsNode 遇到未知工具直接
// NodeRunError，会打断整个 agent run（2026-07-14 实测：active orchestrator 读到共享 system
// prompt「所有角色统一」的凭证协议描述，误以为自己有 write_credential，一调用即让整次扫描 abort，
// 而 orchestrator 角色本就无该工具——设计上就该只读不写）。设了此 handler 后，报错回灌给模型当作
// 该次工具调用的结果，模型据此换个可用工具重试，run 本身不中断——这是该类问题的通用兜底，不依赖
// 每处 prompt 文案与工具清单精确同步。
func unknownToolsHandler(roleID string, logger zerolog.Logger) func(ctx context.Context, name, input string) (string, error) {
	return func(_ context.Context, name, _ string) (string, error) {
		logger.Warn().Str("role", roleID).Str("tool", name).
			Msg("模型调用了未注册的工具（工具清单与 prompt 不一致，或模型幻觉）——已兜底回灌，run 不中断")
		return fmt.Sprintf("工具 %q 不存在，你当前角色没有权限调用它，或它根本不存在。检查自己的工具清单，换一个可用工具继续。", name), nil
	}
}
