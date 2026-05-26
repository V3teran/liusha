// Package endpoint 是 endpoint 表的 Go 模型与持久化层。
//
// active 模式攻击面注册表：commander recon 阶段 write_endpoint 把识别的功能模块
// 沉淀为结构化 fact，graph projector 把它投影成 endpoint 节点（不再仅依赖 finding 反推）。
//
// 设计反思（0056→0057）：早期方案有 status 字段，但漏洞挖掘是 context 相关的反复试验过程
// （同一 endpoint 在不同凭证下挖洞结果不同）。status 字段会阻止合理的 chaining re-spawn，
// 0057 删除。对齐业界（OWASP ZAP / Burp / Caido）的"节点持久 + 多次扫描叠加"模式，
// 前端染色用 projector 派生（关联 finding → vulnerable）。
//
// 跟 http_flow / write_note / finding 的边界：
//   - http_flow:  passive 流水账（每请求 1 行，含 body）—— 不写本表
//   - write_note: LLM 自由文本思考（Redis 草稿）—— 互补关系，note 记推理，endpoint 记 fact
//   - finding:    漏洞元数据（漏洞所在 path）—— endpoint 是攻击面，finding 是漏洞，正交
package endpoint

import "time"

// Endpoint 是 endpoint 表行的 Go 表示。
//
// Path 已模板化（/user/1 → /user/:id），由 caller（WriteEndpoint 工具）规范化后写入；
// 模板化逻辑复用 endpoint.TemplatizePath，避免相同语义 endpoint 因 ID 变化分裂成多行。
//
// Name 是界面上的功能名字（如 "Reflected XSS"），sitemap 视图展示用；可空（API 类
// endpoint 无对应界面名时 NULL，前端 fallback 显示 method + path）。
type Endpoint struct {
	ID           string
	OwnerID      string
	Host         string
	Method       string // 大写（GET / POST），caller 规范化
	Path         string
	Name         string // 界面功能名（可空）
	DiscoveredAt time.Time
}
