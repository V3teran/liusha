// Package credential 管理活凭证（live credentials）：按 host 维度索引身份与认证材料，
// 由 Redis 后端持久化。GetIdentitiesByHost 总是自动注入一个 anonymous 身份，
// 保证调用方在零存储情况下也能拿到“匿名访问”这一基线身份。
package credential

// AnonymousName 是匿名身份固定名，业务上代表“未登录 / 无凭证”访问。
// anonymous 不会被持久化，由 Provider 在读取路径上即时注入。
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
