package llm

import "strings"

// Operation 是协议上的一个操作（连接诊断与地址预览关心的两个）。
type Operation string

// 协议操作。
const (
	OpChat   Operation = "chat"   // 对话：OpenAI 兼容 /chat/completions，Anthropic /v1/messages
	OpModels Operation = "models" // 模型列表：OpenAI 兼容 /models，Anthropic /v1/models
)

// OperationURL 返回 SDK 对该操作实际请求的完整地址——与两个 SDK 的拼接规则一致
// （OpenAI 兼容：去尾斜杠后直接拼操作路径，Base URL 自带版本段；Anthropic：Base URL
// 不含版本段，SDK 拼 v1/…）。只用于展示与诊断，不参与发请求。
func OperationURL(protocol, baseURL string, op Operation) string {
	base := strings.TrimRight(baseURL, "/")
	if protocol == ProviderTypeAnthropic {
		if op == OpModels {
			return base + "/v1/models"
		}
		return base + "/v1/messages"
	}
	if op == OpModels {
		return base + "/models"
	}
	return base + "/chat/completions"
}
