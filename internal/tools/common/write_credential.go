package common

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/credential"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
)

// WriteCredential — 把当前 hunter 拿到的活凭证写入本 host 的 redis credentials key，
// 供同 owner 下其他 hunter（同辈 striker / 父 commander / 后续 task）通过 read_credentials 共享。
//
// 与 read_credentials 对称：read 拉、write 录；同一份 `credentials:<host>` redis hash。
//
// 设计要点：
//   - host 由 builder 注入，LLM 不传参（防串库）
//   - name 由 LLM 提供（通常 = 登录账号名 admin/test/...；SSO 用 sub/email；无账号兜底 "_live_<task_short>"）
//   - 拒绝 name="anonymous"（与 RedisProvider.BatchSave 防御一致；anonymous 是测试概念非持久身份）
//   - credentials 数组任意条任意位置（type ∈ headers/query/body），LLM 自主识别
//   - ttl=0 永久写入，与现有 e2e 录入路径一致；owner 关闭时由外部清理（不在工具里管）
//
// 写入语义：同 name 直接 overwrite（HSET 字段覆盖），LLM 可重复调用更新失效 token。
type WriteCredential struct {
	Provider credential.Provider
	Host     string // builder 注入；空时 Execute 报错
}

// Name 返回工具名 "write_credential"。
func (a *WriteCredential) Name() string { return "write_credential" }

func (a *WriteCredential) Description() string {
	return "把当前 hunter 拿到的活凭证录入本 host 凭证池，让同 owner 下其他 hunter（commander/striker/后续 task）通过 read_credentials 共享。" +
		"\n\n**何时用**：自己刚通过登录 / OAuth / API key 注入等方式获得一组真实凭证，需要让其他 hunter（特别是 spawn 的 striker）也用上时。" +
		"\n\n**调用流程（重要）**：" +
		"\n1. **先调 `read_credentials`** 看本 host 已有身份的 credentials 结构（type/key 怎么填）；" +
		"\n2. 有现存身份 → **模仿其结构**填本工具 credentials 数组（key 名称对齐）；" +
		"\n3. 无现存身份 → 自己识别哪些字段是凭证（headers 里的 Cookie/Authorization/X-Auth-Token、body 里的 csrf_token/session、query 里的 api_key 等），逐条录入。" +
		"\n\n**凭证不只是 cookie**：可能是多条（Cookie + CSRF + Authorization 同时），可能在不同位置（headers/query/body 自由组合），每条 type+key+value 三元组单独一项。" +
		"\n\n**name 字段**：填**登录账号名**（如 admin/test/m233241）；SSO/OAuth 场景填 sub claim 或 email；完全无账号但要存（如 anonymous session 调试）兜底 '_live_<短 task_id>'。**禁止 name='anonymous'**（保留语义不持久化）。" +
		"\n\n返回 {saved: true, name, host}。同 name 重复调用直接覆盖（活凭证刷新场景）。"
}

// ParametersJSON 给 LLM 三参数 schema：name / role / credentials 数组。
func (a *WriteCredential) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "name":{"type":"string","minLength":1,"description":"身份名（登录账号名优先；SSO 用 sub/email；兜底 '_live_<short>'）。禁止 'anonymous'。"},
    "role":{"type":"string","description":"业务角色（admin / user / guest / api / 自定义）。可选，便于上层授权矩阵推断。"},
    "credentials":{
      "type":"array",
      "minItems":1,
      "items":{
        "type":"object",
        "properties":{
          "type":{"type":"string","enum":["headers","query","body"],"description":"凭证注入位置：headers=请求头（Cookie/Authorization），query=URL 参数（api_key），body=请求体字段（csrf_token）"},
          "key":{"type":"string","minLength":1,"description":"字段名（如 'Cookie' / 'Authorization' / 'api_key'）。原样保留大小写。"},
          "value":{"type":"string","description":"字段完整值（如 'PHPSESSID=abc; security=low' / 'Bearer eyJ...' / 'xyz123'）。"}
        },
        "required":["type","key","value"]
      }
    }
  },
  "required":["name","credentials"]
}`)
}

// Execute 校验参数 → 构造 Identity → BatchSave({host:[id]}, 0)。
func (a *WriteCredential) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	if a.Host == "" {
		return toolfx.Result{}, fmt.Errorf("write_credential: Host 未注入（builder 装配缺漏）")
	}
	if a.Provider == nil {
		return toolfx.Result{}, fmt.Errorf("write_credential: Provider nil")
	}

	var in struct {
		Name        string                  `json:"name"`
		Role        string                  `json:"role"`
		Credentials []credential.Credential `json:"credentials"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 write_credential 参数失败: %w", err)
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		return toolfx.Result{}, fmt.Errorf("name 必填且非空")
	}
	if name == credential.AnonymousName {
		return toolfx.Result{}, fmt.Errorf("name='%s' 是测试占位概念，不允许持久化（要测匿名调 read_credentials 拿模板自己替换 value 为 'lstoken'）",
			credential.AnonymousName)
	}
	if len(in.Credentials) == 0 {
		return toolfx.Result{}, fmt.Errorf("credentials 数组至少 1 条")
	}
	for i, c := range in.Credentials {
		switch c.Type {
		case credential.TypeHeaders, credential.TypeQuery, credential.TypeBody:
		default:
			return toolfx.Result{}, fmt.Errorf("credentials[%d].type=%q 非法（必须 headers/query/body）", i, c.Type)
		}
		if strings.TrimSpace(c.Key) == "" {
			return toolfx.Result{}, fmt.Errorf("credentials[%d].key 必填且非空", i)
		}
	}

	id := credential.Identity{
		Name:        name,
		Role:        strings.TrimSpace(in.Role),
		Credentials: in.Credentials,
	}
	// ttl=0 永久；活凭证 owner 关闭时由外部清理（不在工具里管），与 e2e 预录入路径语义一致。
	if err := a.Provider.BatchSave(ctx, map[string][]credential.Identity{a.Host: {id}}, 0); err != nil {
		return toolfx.Result{}, fmt.Errorf("写入 credential 失败: %w", err)
	}

	out, _ := json.Marshal(map[string]any{
		"saved": true,
		"name":  name,
		"host":  a.Host,
		"count": len(in.Credentials),
	})
	summary := fmt.Sprintf("write_credential host=%s name=%s creds=%d", a.Host, name, len(in.Credentials))
	return toolfx.Result{Output: out, Summary: summary}, nil
}
