// Package vuln 是漏洞类 SkillBuilder（sqli / bac / 未来 xss / cmdi 等）的共享层：
// 提供两个子包都用到的 user prompt 拼装、host_lesson 读取、流量详情渲染等纯逻辑。
//
// 子包结构：
//   - vuln/sqli：SQLi SkillBuilder 闭包（容器化沙箱 + tooling SKILL）
//   - vuln/bac ：BAC  SkillBuilder 闭包（compute_similarity 主导）
//
// 此根包仅放无状态 helper，避免在两个子包里散落同样的代码。
package vuln

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/skill"
)

// 子 ReAct 兜底参数。budget 不放 SKILL.md frontmatter，固化在代码层。
const (
	DefaultSubMaxSteps       = 15
	SubWatchdogSeconds       = 60
	DefaultResultCompressDir = "./engagement-store"
	// FlowBodyPromptLimit 与 replay_matrix body_hint 阈值对齐（2KB）。
	FlowBodyPromptLimit = 2000
	// LessonsPromptLimit 是 host_lesson top-N 上限（按 priority 倒序）。
	LessonsPromptLimit = 20
)

// BuildUserPrompt 拼接子 ReAct 的第一条 user message。
//
// skillLabel 用于"按 X SKILL.md 建议流程行动"提示（如 "BAC" / "SQLi"）；
// lessonsBlock 来自 LoadLessonsForPrompt，空字符串时跳过 host 历史经验段。
//
// flow 详情（headers + body）一律完整摆出来，让 LLM 自识别注入点 / 凭证位 /
// 资源归属——agentic 路线不依赖代码层 helper 工具做语义提取。
func BuildUserPrompt(p skill.BuilderParams, skillLabel, lessonsBlock string) string {
	var b strings.Builder
	b.WriteString("测试 flow_id=" + strconv.FormatInt(p.FlowID, 10) +
		" host=" + p.Host + " " + p.Method + " " + p.URL +
		"。按 " + skillLabel + " SKILL.md 建议流程行动，不要文本回答。\n\n")
	b.WriteString(FormatFlowDetail(p.Method, p.URL, p.RequestHeaders, p.RequestBody))
	if lessonsBlock != "" {
		b.WriteString("\n\n## Host 历史经验（跨 engagement 长期知识库，可能含旧情报；带具体 payload/手法可直接复用）\n")
		b.WriteString(lessonsBlock)
	}
	return b.String()
}

// FormatFlowDetail 把 flow 三件套（method/url/headers/body）渲成 markdown 段落。
// body 超过 FlowBodyPromptLimit 时仅保留前缀并标注截断信息。
func FormatFlowDetail(method, url string, headers json.RawMessage, body []byte) string {
	var b strings.Builder
	b.WriteString("## 流量详情\n\n")
	b.WriteString("```\n")
	b.WriteString(strings.ToUpper(method))
	b.WriteString(" ")
	b.WriteString(url)
	b.WriteString("\n```\n\n")

	b.WriteString("### Request Headers\n\n")
	if len(headers) == 0 {
		b.WriteString("（无 headers）\n")
	} else if pretty, err := json.MarshalIndent(headers, "", "  "); err == nil && len(pretty) > 0 {
		b.WriteString("```json\n")
		b.Write(pretty)
		b.WriteString("\n```\n")
	} else {
		b.WriteString("```\n")
		b.Write(headers)
		b.WriteString("\n```\n")
	}

	b.WriteString("\n### Request Body")
	switch {
	case len(body) == 0:
		b.WriteString("\n\n（空）\n")
	case len(body) > FlowBodyPromptLimit:
		fmt.Fprintf(&b, "（截断到前 %d 字节，原总长 %d）\n\n", FlowBodyPromptLimit, len(body))
		b.WriteString("```\n")
		b.Write(body[:FlowBodyPromptLimit])
		b.WriteString("\n```\n")
	default:
		b.WriteString("\n\n```\n")
		b.Write(body)
		b.WriteString("\n```\n")
	}
	return b.String()
}

// LoadLessonsForPrompt 同步读 host_lesson top-N 拼成可读文本。
// store 为 nil / host 为空 / 查询失败 → 返空字符串（让子 ReAct 退化到无 lesson 形态）。
func LoadLessonsForPrompt(ctx context.Context, store *lesson.Store, host string) string {
	if store == nil || host == "" {
		return ""
	}
	lessons, err := store.ListByHost(ctx, "default", host, LessonsPromptLimit)
	if err != nil || len(lessons) == 0 {
		return ""
	}
	var b strings.Builder
	for i, l := range lessons {
		fmt.Fprintf(&b, "%d. (priority=%d, hits=%d) %s\n", i+1, l.Priority, l.HitCount, l.Content)
	}
	return b.String()
}
