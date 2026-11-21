// credential_handler.go 凭证库 HTTP 层：资源化 CRUD + 枚举 + 会话清理。
//
// 安全口径：
//   - 凭证值全量明文返回——本库面向渗透测试操作台，操作者就是要看/复制
//     凭证原文（半掩形态对 Cookie/Token 的可用性为零）；
//   - 全部变更操作（写入/删除/清理）落 audit_log——读不审计（与流量/
//     漏洞读取同口径），改必审计。
//
// 路由（资源式，host 经 CanonicalHost 归一后作为存储键）：
//
//	GET    /credentials?search=&page=&size=    → 登录身份分页列表（跨 host，全量明文 + 会话任务状态）
//	PUT    /credentials/:host/usernames          → upsert 单个登录身份
//	DELETE /credentials/:host/usernames/:username → 删除单个登录身份

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/logx"
)

// credLog 是凭证库 HTTP 层的诊断 logger。
var credLog = logx.New("httpapi.credential")

// CredentialsAPI 是 handlers 对凭证存储的窄接口，仅暴露 HTTP 层真正用到
// 的方法，便于测试 mock。
type CredentialsAPI interface {
	SaveIdentities(ctx context.Context, host string, ids []credential.Identity) error
	GetIdentitiesByHost(ctx context.Context, host string) ([]credential.Identity, error)
	DeleteIdentity(ctx context.Context, host, name string) (bool, error)
	ListHosts(ctx context.Context) ([]credential.HostSummary, error)
}

// CredentialAudit 是凭证库审计出口（*audit.Store 满足）。nil 时跳过审计
// （单测/未装配），生产必配。
type CredentialAudit interface {
	Append(ctx context.Context, e audit.Event) (int64, error)
}

// TaskStatusResolver 把会话身份归属的 task 状态反解给详情视图（会话行显示
// 归属任务是否还在跑）。TaskStatus 返回 (status, exists)；未装配时会话行
// 不带任务状态。
type TaskStatusResolver interface {
	TaskStatus(ctx context.Context, taskID string) (string, bool)
}

// credentialIdentityView 是详情响应行：存储身份 + 派生视图字段。
// kind 由身份名派生（会话/持久），不落存储——单一事实源是名字本身。
type credentialIdentityView struct {
	credential.Identity
	Kind       string `json:"kind"`                  // "session" | "persistent"
	TaskID     string `json:"task_id,omitempty"`     // kind=session 时归属任务
	TaskStatus string `json:"task_status,omitempty"` // 归属任务状态（解析不到时缺省）
}

// credentialRowView 是列表响应行：一个登录身份一行，带上它所在的 host（存储键）。
type credentialRowView struct {
	Host string `json:"host"`
	credentialIdentityView
}

const (
	defaultCredentialPageSize = 50
	maxCredentialPageSize     = 100
)

// hostIdentity 是检索中间态：身份连同它所在的 host。
type hostIdentity struct {
	host string
	id   credential.Identity
}

// listCredentialsHandler 处理 GET /credentials?search=&page=&size= → {items, total}。
//
// 凭证库按 host 分键存储，没有跨 host 的索引：服务端枚举 host、读出各 host 的身份，
// 过滤、排序后只返回当前页——搜索、分页与总数同一口径，浏览器也只拿到当前页的秘密材料。
// 任一 host 读取失败即整体失败：读失败意味着存储不可用，残缺的列表会把「读不到」伪装成「没有」。
func listCredentialsHandler(api CredentialsAPI, resolver TaskStatusResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		page, size := parsePagination(c.Query("page"), c.Query("size"), defaultCredentialPageSize, maxCredentialPageSize)
		needle := strings.ToLower(strings.TrimSpace(c.Query("search")))

		hosts, err := api.ListHosts(ctx)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		var matched []hostIdentity
		for _, h := range hosts {
			ids, err := api.GetIdentitiesByHost(ctx, h.Host)
			if err != nil {
				respondErr(c, http.StatusInternalServerError, err)
				return
			}
			for _, id := range ids {
				// 空壳会话身份不进列表：会话快照的价值在它承载的凭证，没有凭证的空壳只是内部管道痕迹
				if credential.IsSessionIdentity(id.Username) && len(id.Credentials) == 0 {
					continue
				}
				if needle != "" && !credentialMatches(h.Host, id, needle) {
					continue
				}
				matched = append(matched, hostIdentity{host: h.Host, id: id})
			}
		}
		sortCredentialRows(matched)

		total := len(matched)
		start := min((page-1)*size, total)
		end := min(start+size, total)
		items := make([]credentialRowView, 0, end-start)
		for _, m := range matched[start:end] {
			items = append(items, credentialRowView{Host: m.host, credentialIdentityView: identityView(ctx, m.id, resolver)})
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
	}
}

// credentialMatches 判定身份是否命中关键词（needle 已小写）：主机、用户名、角色、凭证键与值，
// 不区分大小写的子串匹配。凭证以「键=值」参与，既能搜键名也能搜值。
func credentialMatches(host string, id credential.Identity, needle string) bool {
	fields := []string{host, id.Username, id.Role}
	for _, cr := range id.Credentials {
		fields = append(fields, cr.Key+"="+cr.Value)
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), needle) {
			return true
		}
	}
	return false
}

// sortCredentialRows 按录入时间倒序（新录入、新会话在前；缺录入时间的历史数据排最后），
// 同时刻按 host、用户名升序——顺序确定，翻页不跳行。
func sortCredentialRows(rows []hostIdentity) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch {
		case a.id.CreatedAt != nil && b.id.CreatedAt != nil && !a.id.CreatedAt.Equal(*b.id.CreatedAt):
			return a.id.CreatedAt.After(*b.id.CreatedAt)
		case (a.id.CreatedAt == nil) != (b.id.CreatedAt == nil):
			return a.id.CreatedAt != nil
		case a.host != b.host:
			return a.host < b.host
		default:
			return a.id.Username < b.id.Username
		}
	})
}

// identityView 派生身份的视图字段：种类由用户名判定；会话身份带归属任务及其状态
// （只给当前页的行解析，不为整库逐条查任务）。
func identityView(ctx context.Context, id credential.Identity, resolver TaskStatusResolver) credentialIdentityView {
	v := credentialIdentityView{Identity: id, Kind: "persistent"}
	if !credential.IsSessionIdentity(id.Username) {
		return v
	}
	v.Kind = "session"
	taskID, ok := credential.SessionTaskID(id.Username)
	if !ok {
		return v
	}
	v.TaskID = taskID
	if resolver != nil {
		if status, exists := resolver.TaskStatus(ctx, taskID); exists {
			v.TaskStatus = status
		} else {
			v.TaskStatus = "gone" // 任务行已不存在（级联删除）
		}
	}
	return v
}

// upsertCredentialIdentityHandler 处理 PUT /credentials/:host/usernames。
// 请求体是单个登录身份（username 必填、credentials 至少一条且 type/key/value 齐全）。
func upsertCredentialIdentityHandler(api CredentialsAPI, aud CredentialAudit) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawHost := c.Param("host")
		host := credential.CanonicalHost(rawHost)
		if host == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "host required"})
			return
		}
		var id credential.Identity
		if err := c.ShouldBindJSON(&id); err != nil {
			respondErr(c, http.StatusBadRequest, err)
			return
		}
		// 身份名先归一（与存储边界同口径：trim + 小写）再校验——否则
		// "Task:t1:session" 会绕过保留名校验、落库时才被归一成保留名。
		id.Username = strings.ToLower(strings.TrimSpace(id.Username))
		if id.Username == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "username required"})
			return
		}
		if credential.IsSessionIdentity(id.Username) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "会话用户名（task:…:session）是系统保留命名，请换一个用户名"})
			return
		}
		if len(id.Credentials) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "credentials 至少一条"})
			return
		}
		for _, cred := range id.Credentials {
			if cred.Key == "" || cred.Value == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "credential 的 key/value 必填"})
				return
			}
			switch cred.Type {
			case credential.TypeHeaders, credential.TypeQuery, credential.TypeBody:
			default:
				c.JSON(http.StatusBadRequest, gin.H{"error": "credential.type 必须是 headers/query/body"})
				return
			}
		}

		if err := api.SaveIdentities(c.Request.Context(), host, []credential.Identity{id}); err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		auditCredential(c.Request.Context(), aud, audit.ActionCredentialSet, host, gin.H{"username": id.Username})
		c.JSON(http.StatusOK, gin.H{"ok": true, "host": host})
	}
}

// deleteCredentialIdentityHandler 处理 DELETE /credentials/:host/usernames/:username。
func deleteCredentialIdentityHandler(api CredentialsAPI, aud CredentialAudit) gin.HandlerFunc {
	return func(c *gin.Context) {
		host := credential.CanonicalHost(c.Param("host"))
		username := c.Param("username")
		if host == "" || username == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "host 与 username 必填"})
			return
		}
		deleted, err := api.DeleteIdentity(c.Request.Context(), host, username)
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		if !deleted {
			c.JSON(http.StatusNotFound, gin.H{"error": "该主机下没有这个登录身份"})
			return
		}
		auditCredential(c.Request.Context(), aud, audit.ActionCredentialDelete, host, gin.H{"username": username})
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// auditCredential 落一条凭证库审计事件（best-effort：失败仅告警不影响主流程，
// 但要留痕——静默丢失等于审计缺口）。
func auditCredential(ctx context.Context, aud CredentialAudit, action, host string, meta gin.H) {
	if aud == nil {
		return
	}
	// 操作者由 RequireAuth 写入 request context；取不到说明装配异常，记为 unknown 而不是丢掉审计
	actor := "unknown"
	if a, ok := audit.ActorFromContext(ctx); ok {
		actor = a
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	if _, err := aud.Append(ctx, audit.Event{
		Actor:      actor,
		Action:     action,
		TargetKind: "credential",
		TargetID:   host,
		Metadata:   raw,
	}); err != nil {
		credLog.Warn().Err(err).Str("host", host).Str("action", action).Msg("凭证库审计写入失败")
	}
}
