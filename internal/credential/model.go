// Package credential 管理活凭证（live credentials）：按 host 维度索引身份与认证材料，
// 由 Redis 后端持久化。
//
// 存储语义：永久存储，零过期机制——不设 key TTL、不设 field TTL、不在载荷里放
// 过期时间。一条凭证该不该删由事实判定（所属任务是否已终态），不由时间猜测；
// 生命周期事件（任务终态清理 / 终态清扫 / 人工删除）是唯一的回收通道。
//
// 身份分两类，由身份名单点编码（命名权威在本包）：
//   - 持久身份：用户或 agent 显式录入的权威凭据，host 级共享，所有任务可见可注入；
//   - 会话身份（task:{taskID}:session）：某次任务执行期的自动会话快照，
//     task 域隔离（读取通道只回显本任务的会话），任务终态后由清理通道回收。
//
// 设计原则：anonymous 不是一个被持久化或预注入的"身份对象"——它是
// LLM 在挖洞时临时构造的测试概念。GetIdentitiesByHost 只返回持久身份与
// 会话身份；测匿名访问时，LLM 自己从任一身份的 credentials 数组拿模板，
// 整段把 value 替换为占位 token（如 "lstoken"）构造重放请求。
package credential

import "time"

// AnonymousName 是匿名身份固定名，业务上代表"未登录 / 无凭证"访问。
// 仅作为 LLM 在 finding 里标注"违规身份"时的字面值约定，不会被持久化也不会
// 被自动注入到 GetIdentitiesByHost 返回值。
const AnonymousName = "anonymous"

// sessionNamePrefix / sessionNameSuffix 是会话身份的命名规则：
// "task:{taskID}:session"。taskID 空时退化为保留名 "session"（无任务上下文
// 的全局会话，判别同样归入会话类）。
const (
	sessionNamePrefix = "task:"
	sessionNameSuffix = ":session"
	globalSessionName = "session"
)

// SessionIdentityName 返回指定任务的会话用户名（保留命名）。
func SessionIdentityName(taskID string) string {
	if taskID == "" {
		return globalSessionName
	}
	return sessionNamePrefix + taskID + sessionNameSuffix
}

// IsSessionIdentity 判别会话用户名（自动快照）与录入用户名（显式录入）。
// 消费方在多个用户名之间选择时应录入优先。
func IsSessionIdentity(name string) bool {
	return name == globalSessionName || hasSessionPrefix(name)
}

func hasSessionPrefix(name string) bool {
	return len(name) > len(sessionNamePrefix) && name[:len(sessionNamePrefix)] == sessionNamePrefix
}

// SessionTaskID 从会话用户名提取归属任务 ID；非会话用户名或无归属任务的
// 全局会话返回 ok=false。
func SessionTaskID(name string) (taskID string, ok bool) {
	if name == globalSessionName || !hasSessionPrefix(name) {
		return "", false
	}
	rest := name[len(sessionNamePrefix):]
	id, _, found := cutLast(rest, sessionNameSuffix)
	if !found || id == "" {
		return "", false
	}
	return id, true
}

// cutLast 按 sep 最后一次出现切分（task ID 含 ":"？——任务 ID 是 UUID，不含
// 冒号，但用 LastIndex 语义对 "task:a:b:session" 这类畸形名更稳健）。
func cutLast(s, sep string) (before, after string, found bool) {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}

// Type 描述凭证注入位置，与 HTTP 请求结构对齐。
// Type 是凭证条目的类型标识。
type Type string

const (
	// TypeHeaders 表示凭证写入请求头（最常见，例如 Cookie / Authorization）。
	TypeHeaders Type = "headers"
	// TypeQuery 表示凭证拼到 URL query 参数。
	TypeQuery Type = "query"
	// TypeBody 表示凭证写入请求体（form / JSON 字段）。
	TypeBody Type = "body"
)

// Credential 是单条凭证条目；同一 Identity 可携带多条（如 Cookie + CSRF token）。
type Credential struct {
	Type  Type   `json:"type"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Identity 表示一个登录身份：真实登录用户名 + 其完整凭证集合。
// 唯一性不变式：同一目标键（host[:port] hash）下 Username 唯一（存储
// field=用户名，同名 upsert 覆盖；SaveIdentities 做 trim+小写归一）。
// Role 为业务语义标签（如 admin / user / guest），上层用于授权矩阵推断。
// 展示用的主机一律取存储键（CanonicalHost 归一的 host[:port]）——不在身份
// 载荷里冗余存"录入原貌"地址（单一事实源；归一键即 Burp Host 列同款语义）。
// CreatedAt 是录入时间（首次出现时刻，存储侧继承，更新不刷新）；
// UpdatedAt 是最近修改时间（每次保存刷新）。两者均为纯观测元数据——
// 不参与任何过期判定（本库零过期机制）。历史数据回填时调用方可显式指定。
type Identity struct {
	Username    string       `json:"username"`
	Role        string       `json:"role"`
	Credentials []Credential `json:"credentials"`
	CreatedAt   *time.Time   `json:"created_at,omitempty"`
	UpdatedAt   *time.Time   `json:"updated_at,omitempty"`
}

// HostSummary 是凭证库枚举（ListHosts）的单行汇总：host 下录入用户名数与
// 会话用户名数。枚举只读 field 名（HKEYS），不触碰任何凭证值——主机清单
// 这一层不应接触秘密材料。
type HostSummary struct {
	Host       string `json:"host"`
	Usernames int    `json:"usernames"` // 录入用户名数
	Sessions  int    `json:"sessions"`  // 会话快照数
}
