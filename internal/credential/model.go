// Package credential 管理活凭证（live credentials）：按 host 维度索引身份与认证材料，
// 由 Redis 后端持久化。
//
// 设计原则（v0024 之后）：anonymous 不是一个被持久化或预注入的"身份对象"——它是
// LLM 在挖洞时临时构造的测试概念。GetIdentitiesByHost 只返回预录入的真实身份；
// 测匿名访问时，LLM 自己从任一身份的 credentials 数组拿模板，整段把 value
// 替换为占位 token（如 "lstoken"）构造重放请求。
package credential

// AnonymousName 是匿名身份固定名，业务上代表"未登录 / 无凭证"访问。
// 仅作为 LLM 在 finding 里标注"违规身份"时的字面值约定，不会被持久化也不会
// 被自动注入到 GetIdentitiesByHost 返回值。
const AnonymousName = "anonymous"

// CredentialType 描述凭证注入位置，与 HTTP 请求结构对齐。
type CredentialType string

const (
	// TypeHeaders 表示凭证写入请求头（最常见，例如 Cookie / Authorization）。
	TypeHeaders CredentialType = "headers"
	// TypeQuery 表示凭证拼到 URL query 参数。
	TypeQuery CredentialType = "query"
	// TypeBody 表示凭证写入请求体（form / JSON 字段）。
	TypeBody CredentialType = "body"
)

// Credential 是单条凭证条目；同一 Identity 可携带多条（如 Cookie + CSRF token）。
type Credential struct {
	Type  CredentialType `json:"type"`
	Key   string         `json:"key"`
	Value string         `json:"value"`
}

// Identity 表示一个具名身份及其完整凭证集合。
// Role 为业务语义标签（如 admin / user / guest），上层用于授权矩阵推断。
type Identity struct {
	Name        string       `json:"name"`
	Role        string       `json:"role"`
	Credentials []Credential `json:"credentials"`
}
