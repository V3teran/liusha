package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/endpoint"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
)

// EndpointStore 是 WriteEndpoint 工具依赖的最小接口。
// *endpoint.Store 自动满足（internal/endpoint 包）。
type EndpointStore interface {
	Upsert(ctx context.Context, e endpoint.Endpoint) (endpoint.Endpoint, error)
}

// WriteEndpoint — commander 在 recon 阶段把识别的功能模块沉淀为结构化 fact。
//
// 仅 commander 注册。striker / tracker 不写 endpoint（他们的产出是 finding / note）。
//
// 跟 write_note 边界：
//   - write_endpoint：结构化 fact（method + path），可查询/可计数/进 graph
//   - write_note：自由文本思考（参数猜测 / 框架陷阱 / 推理草稿）
//
// 跟 write_finding 不冲突：endpoint 是攻击面，finding 是漏洞。一个 endpoint 可以 0/1/多 finding。
type WriteEndpoint struct {
	Store   EndpointStore
	OwnerID string
	Host    string
}

// Name 返回工具名 "write_endpoint"。
func (a *WriteEndpoint) Name() string { return "write_endpoint" }

func (a *WriteEndpoint) Description() string {
	return "把 recon 阶段识别的功能模块沉淀为结构化 endpoint（攻击面注册表）。" +
		"\n\n【何时调】每识别 1 个**新的独立**功能/endpoint（如登录页 / 用户管理 / API 文档 / 上传接口）就调一次。recon 完整性约束的「沉淀度」要求每个独立模块都有 endpoint 记录。" +
		"\n【何时不调】同一 endpoint 已写过（dedup by host+method+path）；非结构化观察用 write_note。" +
		"\n【name 字段（强烈推荐传）】界面功能名（如 \"Reflected XSS\" / \"用户管理\"）。sitemap 投影把 endpoint 显示为攻击面节点，叶节点 label 优先用 name、fallback 才用冷冰冰的 method+path——有 name 才看得懂攻击面。同 endpoint 多次 write 时 name 可更新（commander 后续 recon 补名）。"
}

// ParametersJSON 给出 method + path + name 三参数 schema。
func (a *WriteEndpoint) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "method":{"type":"string","description":"HTTP 方法（GET / POST / PUT / DELETE / PATCH / HEAD / OPTIONS），大小写不敏感"},
    "path":{"type":"string","description":"endpoint 路径（如 /admin/users 或 /api/v2/orders）。带 ID 段（/user/1）会自动模板化为 /user/:id 去重"},
    "name":{"type":"string","description":"功能名字（界面/nav/title 上的标签，如 \"Reflected XSS\"、\"用户管理\"）。可选但强推荐——从 commander recon 浏览页面时看到的 nav/title/breadcrumb 提取。API 类无界面名可省略。"}
  },
  "required":["method","path"]
}`)
}

// Execute 解析 method + path + name → 模板化 path → Store.Upsert → 返回 {id, path, name}。
func (a *WriteEndpoint) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 write_endpoint 参数失败: %w", err)
	}
	if in.Method == "" {
		return toolfx.Result{}, fmt.Errorf("method 必填")
	}
	if in.Path == "" {
		return toolfx.Result{}, fmt.Errorf("path 必填")
	}

	// 模板化 path（与 sitemap 投影一致，避免 /user/1 和 /user/2 误判为两个 endpoint）
	tpl := endpoint.TemplatizePath(in.Path)

	saved, err := a.Store.Upsert(ctx, endpoint.Endpoint{
		OwnerID: a.OwnerID,
		Host:    a.Host,
		Method:  in.Method,
		Path:    tpl,
		Name:    in.Name,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("保存 endpoint 失败: %w", err)
	}

	out, _ := json.Marshal(map[string]string{
		"id":   saved.ID,
		"path": saved.Path,
		"name": saved.Name,
	})
	summary := fmt.Sprintf("write_endpoint %s %s name=%q", saved.Method, saved.Path, saved.Name)
	return toolfx.Result{Output: out, Summary: summary}, nil
}
