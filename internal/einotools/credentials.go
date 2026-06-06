package einotools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"

	"github.com/V3teran/liusha/internal/credential"
)

// CredentialReader / CredentialWriter 是窄接口，credential.Provider 自动满足。
type CredentialReader interface {
	GetIdentitiesByHost(ctx context.Context, host string) ([]credential.Identity, error)
}

type CredentialWriter interface {
	BatchSave(ctx context.Context, byHost map[string][]credential.Identity, ttlSeconds int) error
}

// BuildReadCredentials 造原生 eino read_credentials 工具。host 闭包捕获，LLM 不传参（防串库）。
func BuildReadCredentials(store CredentialReader, host string) (tool.BaseTool, error) {
	return utils.InferTool(
		"read_credentials",
		"拉取本 task 目标 host 的全部预录入真实身份（含 cookie/token/body 字段值，可直接拼请求）。"+
			"**何时用**：流量请求带 cookie/token/Authorization 等认证字段，且你想用其他身份重放（测越权、"+
			"复用 admin 看完整数据）。公开接口（请求无任何认证字段）不需要调用。"+
			"返回 [{name, role, credentials: [{type, key, value}]}] —— type ∈ headers/query/body。"+
			"**返回里不含 anonymous**——anonymous 是测试概念不是持久化身份。"+
			"测匿名/未授权访问的两种路径："+
			"(A) 列表里有 ≥1 个真实身份 → 拿任一身份的 credentials 数组作模板，每条 value 整段替换为 'lstoken'（不保留 name= 前缀），构造 anonymous 重放请求；"+
			"(B) 列表为空（无任何预录入身份）→ 自己看原始流量识别哪些字段是认证字段（headers Cookie/Authorization、query token、body password 等），整段替换为 'lstoken'。"+
			"用 'lstoken' 占位（而非完全无凭证）能精确触发服务端『token 校验失败』分支，"+
			"vs 完全无 cookie 走『未登录』分支——两个分支处理可能不同，只测后者会漏真实认证缺陷。",
		func(ctx context.Context, _ noArgs) ([]credential.Identity, error) {
			if host == "" {
				return nil, errors.New("read_credentials: Host 注入缺失")
			}
			ids, err := store.GetIdentitiesByHost(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("拉取 %s 凭证失败: %w", host, err)
			}
			return ids, nil
		})
}

// credItem 是 write_credential 单条凭证入参（type 枚举 headers/query/body）。
type credItem struct {
	Type  string `json:"type"  jsonschema:"required,enum=headers,enum=query,enum=body,description=凭证注入位置：headers=请求头（Cookie/Authorization），query=URL 参数，body=请求体字段"`
	Key   string `json:"key"   jsonschema:"required,description=字段名（如 Cookie / Authorization / api_key）。原样保留大小写。"`
	Value string `json:"value" jsonschema:"description=字段完整值（如 PHPSESSID=abc; security=low / Bearer eyJ... / xyz123）。"`
}

// writeCredentialArgs 是 write_credential 入参。
type writeCredentialArgs struct {
	Name        string     `json:"name"        jsonschema:"required,description=身份名（登录账号名优先；SSO 用 sub/email；兜底 _live_<short>）。禁止 anonymous。"`
	Role        string     `json:"role"        jsonschema:"description=业务角色（admin / user / guest / api / 自定义）。可选，便于上层授权矩阵推断。"`
	Credentials []credItem `json:"credentials" jsonschema:"required,description=凭证数组，至少 1 条；每条 type/key/value。"`
}

// BuildWriteCredential 造原生 eino write_credential 工具。host 闭包捕获。
func BuildWriteCredential(store CredentialWriter, host string) (tool.BaseTool, error) {
	return utils.InferTool(
		"write_credential",
		"把当前 hunter 拿到的活凭证录入本 host 凭证池，让同 owner 下其他 hunter（commander/striker/后续 task）通过 read_credentials 共享。"+
			"\n\n**何时用**：自己刚通过登录 / OAuth / API key 注入等方式获得一组真实凭证，需要让其他 hunter（特别是 spawn 的 striker）也用上时。"+
			"\n\n**先调 `read_credentials`** 看本 host 已有身份的 credentials 结构：有就**模仿其 type/key**填（key 对齐，避免一 host 两套 schema）；没有就自己从流量识别认证字段逐条录入。"+
			"\n\n返回 {saved: true, name, host}。同 name 重复调用直接覆盖（活凭证刷新场景）。",
		func(ctx context.Context, in writeCredentialArgs) (map[string]any, error) {
			if host == "" {
				return nil, errors.New("write_credential: Host 注入缺失")
			}
			name := strings.TrimSpace(in.Name)
			if name == "" {
				return nil, errors.New("name 必填且非空")
			}
			if name == credential.AnonymousName {
				return nil, fmt.Errorf("name='%s' 是测试占位概念，不允许持久化（要测匿名调 read_credentials 拿模板自己替换 value 为 'lstoken'）",
					credential.AnonymousName)
			}
			if len(in.Credentials) == 0 {
				return nil, errors.New("credentials 数组至少 1 条")
			}
			creds := make([]credential.Credential, 0, len(in.Credentials))
			for i, c := range in.Credentials {
				ct := credential.CredentialType(c.Type)
				switch ct {
				case credential.TypeHeaders, credential.TypeQuery, credential.TypeBody:
				default:
					return nil, fmt.Errorf("credentials[%d].type=%q 非法（必须 headers/query/body）", i, c.Type)
				}
				if strings.TrimSpace(c.Key) == "" {
					return nil, fmt.Errorf("credentials[%d].key 必填且非空", i)
				}
				creds = append(creds, credential.Credential{Type: ct, Key: c.Key, Value: c.Value})
			}

			id := credential.Identity{Name: name, Role: strings.TrimSpace(in.Role), Credentials: creds}
			// ttlSeconds=0 永久；活凭证 owner 关闭时由外部清理，与 e2e 预录入路径语义一致。
			if err := store.BatchSave(ctx, map[string][]credential.Identity{host: {id}}, 0); err != nil {
				return nil, fmt.Errorf("写入 credential 失败: %w", err)
			}
			return map[string]any{"saved": true, "name": name, "host": host, "count": len(creds)}, nil
		})
}
