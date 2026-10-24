// credential_handler.go 凭证库 HTTP 层：资源化 CRUD + 枚举 + 会话清理。
//
// 安全口径：
//   - 凭证值全量明文返回——本库面向渗透测试操作台，操作者就是要看/复制
//     凭证原文（半掩形态对 Cookie/Token 的可用性为零）；
//   - 枚举（GET /credentials）只读 field 名，服务端也不触碰凭证值；
//   - 全部变更操作（写入/删除/清理）落 audit_log——读不审计（与流量/
//     漏洞读取同口径），改必审计。
//
// 路由（资源式，host 经 CanonicalHost 归一后作为存储键）：
//
//	GET    /credentials                        → host 汇总列表
//	GET    /credentials/:host                  → 身份详情（全量明文 + 会话任务状态）
//	PUT    /credentials/:host/usernames          → upsert 单个登录身份
//	DELETE /credentials/:host/usernames/:username → 删除单个登录身份
//	DELETE /credentials/:host                  → 清空该 host 全部身份
//	DELETE /credentials/:host/sessions         → 仅清会话身份
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
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
	DeleteIdentity(ctx context.Context, host, name string) error
	DeleteSessions(ctx context.Context, host string) (int, error)
	DeleteHost(ctx context.Context, host string) error
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
	Kind       string `json:"kind"`                 // "session" | "persistent"
	TaskID     string `json:"task_id,omitempty"`    // kind=session 时归属任务
	TaskStatus string `json:"task_status,omitempty"` // 归属任务状态（解析不到时缺省）
}

// listCredentialHostsHandler 处理 GET /credentials。
func listCredentialHostsHandler(api CredentialsAPI, aud CredentialAudit) gin.HandlerFunc {
	return func(c *gin.Context) {
		hosts, err := api.ListHosts(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if hosts == nil {
			hosts = []credential.HostSummary{}
		}
		c.JSON(http.StatusOK, gin.H{"hosts": hosts})
	}
}

// getCredentialDetailHandler 处理 GET /credentials/:host（全量明文）。
func getCredentialDetailHandler(api CredentialsAPI, resolver TaskStatusResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		host := credential.CanonicalHost(c.Param("host"))
		if host == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "host required"})
			return
		}

		ids, err := api.GetIdentitiesByHost(c.Request.Context(), host)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		views := make([]credentialIdentityView, 0, len(ids))
		for _, id := range ids {
			v := credentialIdentityView{Identity: id, Kind: "persistent"}
			if credential.IsSessionIdentity(id.Username) {
				v.Kind = "session"
				if taskID, ok := credential.SessionTaskID(id.Username); ok {
					v.TaskID = taskID
					if resolver != nil {
						if status, exists := resolver.TaskStatus(c.Request.Context(), taskID); exists {
							v.TaskStatus = status
						} else {
							v.TaskStatus = "gone" // 任务行已不存在（级联删除）
						}
					}
				}
			}
			views = append(views, v)
		}
		c.JSON(http.StatusOK, gin.H{"host": host, "identities": views})
	}
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		if err := api.DeleteIdentity(c.Request.Context(), host, username); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		auditCredential(c.Request.Context(), aud, audit.ActionCredentialDelete, host, gin.H{"username": username})
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// deleteCredentialHostHandler 处理 DELETE /credentials/:host。
func deleteCredentialHostHandler(api CredentialsAPI, aud CredentialAudit) gin.HandlerFunc {
	return func(c *gin.Context) {
		host := credential.CanonicalHost(c.Param("host"))
		if host == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "host required"})
			return
		}
		if err := api.DeleteHost(c.Request.Context(), host); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		auditCredential(c.Request.Context(), aud, audit.ActionCredentialDelete, host, gin.H{"scope": "host"})
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// purgeCredentialSessionsHandler 处理 DELETE /credentials/:host/sessions。
func purgeCredentialSessionsHandler(api CredentialsAPI, aud CredentialAudit) gin.HandlerFunc {
	return func(c *gin.Context) {
		host := credential.CanonicalHost(c.Param("host"))
		if host == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "host required"})
			return
		}
		deleted, err := api.DeleteSessions(c.Request.Context(), host)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		auditCredential(c.Request.Context(), aud, audit.ActionCredentialDelete, host, gin.H{"scope": "sessions", "deleted": deleted})
		c.JSON(http.StatusOK, gin.H{"ok": true, "deleted": deleted})
	}
}

// auditCredential 落一条凭证库审计事件（best-effort：失败仅告警不影响主流程，
// 但要留痕——静默丢失等于审计缺口）。
func auditCredential(ctx context.Context, aud CredentialAudit, action, host string, meta gin.H) {
	if aud == nil {
		return
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	if _, err := aud.Append(ctx, audit.Event{
		Actor:      audit.ActorAPIUser,
		Action:     action,
		TargetKind: "credential",
		TargetID:   host,
		Metadata:   raw,
	}); err != nil {
		credLog.Warn().Err(err).Str("host", host).Str("action", action).Msg("凭证库审计写入失败")
	}
}
