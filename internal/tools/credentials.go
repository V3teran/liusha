package tools

import (
	"context"
	"encoding/json"
	"fmt"

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

type readCredentialsTool struct{ deps Deps }

func (t *readCredentialsTool) Name() string            { return "read_credentials" }
func (t *readCredentialsTool) ShortDesc() string       { return "读取本 host 预录入真实身份" }
func (t *readCredentialsTool) Desc() string {
	return "读取本 host 预录入的真实身份（cookie/token/body 字段），可直接拼请求做重放/越权测试。"
}
func (t *readCredentialsTool) Schema() json.RawMessage { return readCredentialsSchema }

func (t *readCredentialsTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Host string `json:"host"`
	}
	_ = json.Unmarshal(args, &a)
	host := a.Host
	if host == "" {
		host = t.deps.Host
	}

	identities, err := t.deps.Creds.GetIdentitiesByHost(ctx, host)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("read_credentials: %v", err)}, nil
	}
	if len(identities) == 0 {
		return registry.ToolResult{Output: "[]"}, nil
	}
	b, _ := json.MarshalIndent(identities, "", "  ")
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
    "name": {"type": "string", "description": "身份名，如 \"admin\" / \"user_alice\"。"},
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
  "required": ["host", "name", "credentials"]
}`)

type writeCredentialTool struct{ deps Deps }

func (t *writeCredentialTool) Name() string            { return "write_credential" }
func (t *writeCredentialTool) ShortDesc() string       { return "录入活凭证" }
func (t *writeCredentialTool) Desc() string {
	return "把刚拿到的活凭证录入本 host 凭证池，供同 owner 下其他 executor 共享复用。"
}
func (t *writeCredentialTool) Schema() json.RawMessage { return writeCredentialSchema }

func (t *writeCredentialTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Host        string `json:"host"`
		Name        string `json:"name"`
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
			Type:  credential.CredentialType(c.Type),
			Key:   c.Key,
			Value: c.Value,
		})
	}
	identity := credential.Identity{Name: a.Name, Role: a.Role, Credentials: creds}

	if err := t.deps.Creds.BatchSave(ctx, map[string][]credential.Identity{a.Host: {identity}}, 0); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_credential: %v", err)}, nil
	}
	return registry.ToolResult{Output: fmt.Sprintf("凭证已写入: host=%s name=%s", a.Host, a.Name)}, nil
}
