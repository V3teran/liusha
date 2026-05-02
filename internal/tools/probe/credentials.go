package probe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/credential"
)

// FetchCredentials — BAC ReAct 第一步：拉目标 host 全部活身份（含 anonymous）。
//
// 输出仅含 (name, role, has_credentials)，不暴露 raw cred value，避免：
//   - 让 LLM 在 tool message 里看到密钥（token 浪费 + 安全风险）。
//   - 喂给后续 ReplayMultiIdentity 时还得二次序列化。
//
// 全身份留在 Session.Identities 里，供 ReplayMultiIdentity 直接读取。
//
// Locations 来自上游 classify_traffic 输出（经 delegate.BuilderParams 透传）；
// 非空时用 credential.BuildAnonymous(Locations) 替换 Provider 注入的"完全无凭证 anonymous"，
// 让重放时 anonymous 携带占位 token 触发服务端的"token 校验失败"分支。
type FetchCredentials struct {
	Provider  credential.Provider
	Session   *Session
	Locations []credential.CredentialLocation
}

// Name 返回动作名 "fetch_credentials"。
func (a *FetchCredentials) Name() string { return "fetch_credentials" }

// Description 给 LLM 看的简介。
func (a *FetchCredentials) Description() string {
	return "拉取目标 host 的全部活身份（含 anonymous），仅返回身份名/角色/是否有凭证；raw value 不出 session。"
}

// ParametersJSON 给出 host 必填 schema。
func (a *FetchCredentials) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "host":{"type":"string","description":"目标 host（不含 scheme）"}
  },
  "required":["host"]
}`)
}

// identitySummary 是返回给 LLM 的瘦身份摘要：
//   - 不含 raw value（避免泄漏 + token 节省）。
//   - has_credentials=false 通常对应 anonymous。
type identitySummary struct {
	Name           string `json:"name"`
	Role           string `json:"role,omitempty"`
	HasCredentials bool   `json:"has_credentials"`
}

// fetchOutput 是 Result.Output 的统一结构。
type fetchOutput struct {
	Host       string            `json:"host"`
	Identities []identitySummary `json:"identities"`
}

// Execute 解析 args → Provider.GetIdentitiesByHost → 写 Session → 返回瘦摘要。
func (a *FetchCredentials) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var in struct {
		Host string `json:"host"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Result{}, fmt.Errorf("解析 fetch_credentials 参数失败: %w", err)
	}
	if in.Host == "" {
		return tool.Result{}, fmt.Errorf("host 必填")
	}

	ids, err := a.Provider.GetIdentitiesByHost(ctx, in.Host)
	if err != nil {
		return tool.Result{}, fmt.Errorf("获取身份列表 host=%s: %w", in.Host, err)
	}

	// 若上游传了 credential_locations，用占位 token 重建 anonymous 身份，
	// 让重放时 anonymous 是"假认证请求"而不是"完全无 cookie"——
	// 能精确触发服务端的"token 校验失败"分支，避免漏报。
	if len(a.Locations) > 0 {
		anon := credential.BuildAnonymous(a.Locations)
		replaced := false
		for i, id := range ids {
			if id.Name == credential.AnonymousName {
				ids[i] = anon
				replaced = true
				break
			}
		}
		if !replaced {
			ids = append(ids, anon)
		}
	}

	a.Session.Identities = ids

	out := fetchOutput{Host: in.Host, Identities: make([]identitySummary, 0, len(ids))}
	for _, id := range ids {
		out.Identities = append(out.Identities, identitySummary{
			Name:           id.Name,
			Role:           id.Role,
			HasCredentials: len(id.Credentials) > 0,
		})
	}
	enc, err := json.Marshal(out)
	if err != nil {
		return tool.Result{}, fmt.Errorf("序列化 fetch_credentials 输出失败: %w", err)
	}
	return tool.Result{
		Output:  enc,
		Summary: fmt.Sprintf("fetch_credentials host=%s identities=%d", in.Host, len(ids)),
	}, nil
}
