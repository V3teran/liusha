package lesson

import (
	"context"
	"fmt"
)

// HostGlobalHint 是 hint 的特殊 host 值——表示对所有 host 通用的业务规则。
// 与具体 host 一起被 ListByHostWithGlobalHints 拉取。
const HostGlobalHint = "*"

// defaultHints 是从 sqli/bac SKILL.md 里迁出来的"liusha 业务规则"。
//
// 这些规则不是 LLM 训练数据里的（OWASP 没有 liusha 自定义的"anonymous 优先级"判定），
// 也不是工具协议能表达的，必须有载体。原本写死在 SKILL.md 步骤里，
// 现在数据化为 kind='hint' lesson 行——支持运行时增删、按 priority 排序、
// 与 distill lesson 共享注入路径（LoadLessonsForPrompt 一次取回）。
//
// 借鉴 Cairn Hint 的"业务规则与执行流程解耦"思路（不抄实现）。
var defaultHints = []struct {
	Content  string
	Priority int
}{
	{
		Content: "判 BAC 时：anonymous 成功访问 → 必须判 unauthorized_access（critical），" +
			"即使路径含 /admin/、即使其他低权限身份也成功。" +
			"理由：认证机制失效是比『权限粒度错误』更严重的根因。",
		Priority: 9,
	},
	{
		Content: "判水平越权时必须排除合法 owner：扫 response body 提取所有者字段" +
			"（owner / buyer / seller / user_id / username / created_by / assignee），" +
			"若某 identity.name 等于该字段值，则该身份是合法访问，" +
			"从 violating_identities 中剔除。响应无所有者字段时按全部计入 violators。",
		Priority: 9,
	},
	{
		Content: "判 BAC anonymous 是否成功访问：用 run_command curl 拿 anonymous 响应后，" +
			"对比 body 长度与基线响应（合法用户的）——长度比 < 0.5 时通常是被服务端重定向到登录/欢迎页，" +
			"或返回简化的『请先登录』页面（PHP/Java/Node SSR 常见），**绝不**判 unauthorized_access。" +
			"仅当长度比 >= 0.5 且 body 含真实业务字段（实际数据值，不是表单标签）时 anonymous 才算成功访问。",
		Priority: 9,
	},
	{
		Content: "判 SQLi 时：信号在响应 body 内容里不在 status code。" +
			"500 + body 含数据库 driver 异常（SQLException / pg_query: / ora-00933 等）是强证据；" +
			"403 + body 含 blocked / forbidden 是 WAF 不是无 SQLi。" +
			"绝不把 status != 200 当否定信号。",
		Priority: 8,
	},
	{
		Content: "BAC 越权判定前提：至少 2 个非 anonymous 身份。" +
			"只有 1 个非 anonymous 身份时无法对比，直接 done(no_pattern_match)。",
		Priority: 7,
	},
	{
		Content: "BAC 资源类型不依赖路径模式匹配——看资源本质属性：" +
			"高权限资源（系统配置 / 全局设置 / 所有用户列表 / 批量操作 / 审计日志），" +
			"路径常含 /admin/ /sys/ 但只是辅助信号，最终看响应数据是否反映『全局/系统级』性质；" +
			"低权限私有资源（个人信息 / 订单详情 / 私有文件 / 购物车）URI 常含 uid/orderid/:user_id。" +
			"`/me/profile` 这种『按 caller 取数据』的接口天然不构成水平越权。",
		Priority: 7,
	},
	{
		Content: "finding.summary 是核心：LLM 用自然语言描述发现是什么、怎么验证、推理依据。" +
			"evidence 是可选的结构化补充。kind 是软标签（自由命名，影响 dedup/UI 配色）——" +
			"同 host 同 endpoint 同问题请保持一致命名（看 read_notes / 已有 finding 参考）。",
		Priority: 6,
	},
}

// SeedDefaultHints 在 scanner 启动时同步默认 hint 集合到 lesson 表。
//
// 幂等：依赖 lesson.Add 的 (tenant, host, content_hash) ON CONFLICT 路径——
// 同 content 重启不会重复入库，仅 hit_count++（这正是我们想要的：seed 的 hit_count
// 自然反映启动次数，不影响功能）。
//
// 失败语义：单条 hint Add 失败立即返回首个错误（caller 可选择 warn 不阻断启动）。
func SeedDefaultHints(ctx context.Context, store *Store) error {
	if store == nil {
		return fmt.Errorf("lesson.SeedDefaultHints: store nil")
	}
	for i, h := range defaultHints {
		l := Lesson{
			TenantID: "default",
			Host:     HostGlobalHint,
			Kind:     KindHint,
			Content:  h.Content,
			Priority: h.Priority,
		}
		if _, err := store.Add(ctx, l); err != nil {
			return fmt.Errorf("seed hint %d: %w", i, err)
		}
	}
	return nil
}
