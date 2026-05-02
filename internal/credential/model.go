// Package credential 管理活凭证（live credentials）：按 host 维度索引身份与认证材料，
// 由 Redis 后端持久化。GetIdentitiesByHost 总是自动注入一个 anonymous 身份，
// 保证调用方在零存储情况下也能拿到“匿名访问”这一基线身份。
package credential

// AnonymousName 是匿名身份固定名，业务上代表“未登录 / 无凭证”访问。
// anonymous 不会被持久化，由 Provider 在读取路径上即时注入。
const AnonymousName = "anonymous"

// AnonymousPlaceholderToken 是 anonymous 在认证位置填充的占位 token。
//
// 设计意图（对标 liusha2）：anonymous 不是"完全无 cookie"的请求，而是"带占位 token 的假认证"
// 请求——这样能精确触发服务端的"token 校验失败"分支，而不是走"未登录"分支。
// 如果服务端两条分支返回不同（如未登录直接 200 公开内容，token 失败才 401），
// 完全无 cookie 的 anonymous 会漏掉真正的认证缺陷。
const AnonymousPlaceholderToken = "lstoken"

// CredentialLocation 描述凭证在请求中的位置（Type + Key），不含 Value。
//
// 由 orchestrator (classify_traffic 工具) 通过 LLM 分析流量产出，
// 经 delegate 透传到 BAC 子 ReAct，用于 BuildAnonymous 构造带占位 token 的假认证 anonymous。
type CredentialLocation struct {
	Type CredentialType `json:"type"`
	Key  string         `json:"key"`
}

// BuildAnonymous 根据 credential_locations 构造一个 anonymous 身份。
//
// 当 locations 非空时，每个 location 会生成一条占位 credential（value=AnonymousPlaceholderToken）；
// 当 locations 为空时，返回的 anonymous 不带任何 credential（向后兼容旧行为：完全无 cookie 请求）。
func BuildAnonymous(locations []CredentialLocation) Identity {
	creds := make([]Credential, 0, len(locations))
	for _, loc := range locations {
		creds = append(creds, Credential{
			Type:  loc.Type,
			Key:   loc.Key,
			Value: AnonymousPlaceholderToken,
		})
	}
	return Identity{
		Name:        AnonymousName,
		Role:        AnonymousName,
		Credentials: creds,
	}
}

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
