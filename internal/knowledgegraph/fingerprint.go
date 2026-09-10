package knowledgegraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ComputeActionFingerprint 计算 Action 内容的指纹（SHA256）
//
// 指纹基于：
// - instruction（核心字段）
// - target.domain
// - target.locator
//
// 忽略：
// - priority（优先级不影响去重）
// - depends_on（依赖关系不影响去重）
// - 其他元数据
//
// 参数：
// - content: Action 节点的 Content 字段（JSON）
//
// 返回：
// - string: 64 字符的十六进制 SHA256 哈希
//
// 示例：
//   content := `{"instruction":"scan port 80","target":{"domain":"example.com"}}`
//   fp := ComputeActionFingerprint(json.RawMessage(content))
//   // fp = "abc123..."
func ComputeActionFingerprint(content json.RawMessage) string {
	// 解析 content
	var actionContent struct {
		Instruction string `json:"instruction"`
		Target      struct {
			Domain  string `json:"domain"`
			RefKind string `json:"ref_kind"`
			Locator string `json:"locator"`
		} `json:"target_ref"`
	}

	// 忽略解析错误，使用原始 content 计算哈希
	_ = json.Unmarshal(content, &actionContent)

	// 构建唯一标识字符串
	key := actionContent.Instruction + "|" +
		actionContent.Target.Domain + "|" +
		actionContent.Target.RefKind + "|" +
		actionContent.Target.Locator

	// 如果解析失败，使用原始 content
	if key == "|||" {
		key = string(content)
	}

	// 计算 SHA256
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// ComputeActionFingerprintFromNode 从 Node 计算 Action 指纹
//
// 便捷方法，直接从 Node 提取 Content 并计算指纹
func ComputeActionFingerprintFromNode(node Node) string {
	if node.Kind != KindAction {
		return ""
	}
	return ComputeActionFingerprint(node.Content)
}
