package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/registry"
)

// ─── read_credentials ────────────────────────────────────────────────────────

var readCredentialsSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "host": {"type": "string", "description": "目标 host（不填则用当前任务 host）。"}
  }
}`)

type readCredentialsTool struct {
	registry.BaseTool
	deps Deps
}

func newReadCredentialsTool(deps Deps, timeout time.Duration) *readCredentialsTool {
	t := &readCredentialsTool{deps: deps}
	t.SetTimeout(timeout)
	return t
}

func (t *readCredentialsTool) Name() string      { return "read_credentials" }
func (t *readCredentialsTool) ShortDesc() string { return "读取本 host 预录入真实身份" }
func (t *readCredentialsTool) Desc() string {
	return "读取本 host 预录入的真实身份（cookie/token/body 字段），可直接拼请求做重放/越权测试。"
}
func (t *readCredentialsTool) Schema() json.RawMessage { return readCredentialsSchema }

func (t *readCredentialsTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Host string `json:"host"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_credentials: invalid args: %v", err)}, nil
	}
	host := a.Host
	if host == "" {
		host = t.deps.Host
	}

	identities, err := t.deps.Creds.GetIdentitiesByHost(ctx, host)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_credentials: %v", err)}, nil
	}
	// task 域过滤：本 task 的自动会话身份 + host 级共享身份（无 task: 前缀）。
	// 其他 task 的会话身份不回显——共享凭证库下的跨任务隔离（读取通道）。
	mine := sessionIdentityName(t.deps.TaskID)
	filtered := identities[:0]
	for _, id := range identities {
		if credential.IsSessionIdentity(id.Username) && id.Username != mine {
			continue
		}
		filtered = append(filtered, id)
	}
	identities = filtered
	if len(identities) == 0 {
		return registry.ToolResult{Output: "[]"}, nil
	}
	b, err := json.MarshalIndent(identities, "", "  ")
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_credentials: marshal failed: %v", err)}, nil
	}
	return registry.ToolResult{
		Output: string(b),
		Signal: &registry.Signal{
			Kind:     registry.SignalCredentialDump,
			ToolName: "read_credentials",
			Content:  fmt.Sprintf("返回 %d 条身份", len(identities)),
		},
	}, nil
}

// ─── write_credential ────────────────────────────────────────────────────────

var writeCredentialSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "host": {"type": "string", "description": "归属 host。"},
    "username": {"type": "string", "description": "登录用户名（真实登录用的用户名），如 \"admin\"。"},
    "role": {"type": "string", "description": "业务角色，如 \"admin\" / \"user\" / \"guest\"。"},
    "credentials": {
      "type": "array",
      "description": "凭证列表。",
      "items": {
        "type": "object",
        "properties": {
          "type":  {"type": "string", "enum": ["headers", "query", "body"]},
          "key":   {"type": "string"},
          "value": {"type": "string"}
        },
        "required": ["type", "key", "value"]
      }
    }
  },
  "required": ["host", "username", "credentials"]
}`)

type writeCredentialTool struct {
	registry.BaseTool
	deps Deps
}

func newWriteCredentialTool(deps Deps, timeout time.Duration) *writeCredentialTool {
	t := &writeCredentialTool{deps: deps}
	t.SetTimeout(timeout)
	return t
}

func (t *writeCredentialTool) Name() string      { return "write_credential" }
func (t *writeCredentialTool) ShortDesc() string { return "录入活凭证" }
func (t *writeCredentialTool) Desc() string {
	return "把刚拿到的活凭证录入本 host 凭证池，供同 task 下其他执行者共享复用。" +
		"username 必须是该账号真实登录用户名（如 admin），禁止自造代号" +
		"（admin-low、xxx_r2 这类后缀是噪声）；同一账号先 read_credentials 查既有用户名并原样复用。"
}
func (t *writeCredentialTool) Schema() json.RawMessage { return writeCredentialSchema }

func (t *writeCredentialTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Host        string `json:"host"`
		Username    string `json:"username"`
		Role        string `json:"role"`
		Credentials []struct {
			Type  string `json:"type"`
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "write_credential: 解析参数失败: " + err.Error()}, nil
	}
	if a.Host == "" {
		a.Host = t.deps.Host
	}

	creds := make([]credential.Credential, 0, len(a.Credentials))
	for _, c := range a.Credentials {
		creds = append(creds, credential.Credential{
			Type:  credential.Type(c.Type),
			Key:   c.Key,
			Value: c.Value,
		})
	}
	identity := credential.Identity{Username: a.Username, Role: a.Role, Credentials: creds}

	if err := t.deps.Creds.SaveIdentities(ctx, a.Host, []credential.Identity{identity}); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_credential: %v", err)}, nil
	}
	return registry.ToolResult{Output: fmt.Sprintf("凭证已写入: host=%s username=%s", a.Host, a.Username)}, nil
}
