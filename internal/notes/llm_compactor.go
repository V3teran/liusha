package notes

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/llm"
)

// compactorSystemPrompt 是 LLMCompactor 用的 system 提示词，编译期 embed。
//
//go:embed compactor_prompt.md
var compactorSystemPrompt string

// LLMCompactor 用 light LLM（通常 Haiku）把 N 条老 note 蒸馏成 1 条 summary。
//
// 满足 Compactor 接口，由 cmd/scanner 装配时通过 router.For("inspector") 拿
// 轻量 Generator 注入。
type LLMCompactor struct {
	gen llm.Generator
}

// NewLLMCompactor 构造 LLMCompactor；gen 必填。
func NewLLMCompactor(gen llm.Generator) *LLMCompactor {
	return &LLMCompactor{gen: gen}
}

// compactorHunterID 是蒸馏 entry 的 hunter_id 字段值，方便日后 grep 区分。
const compactorHunterID = "compactor"

// Compact 把 oldEntries 蒸馏成 1 条 summary entry。
//
// 解析每条 entry 的 content 字段拼成编号列表喂给 LLM；忽略无法解析的条目
// （保持容错——历史 entry 格式漂移不应阻断蒸馏）。
//
// 输出包装为标准 entry JSON：{"content":"[蒸馏摘要] <text>","hunter_id":"compactor"}
// LLM 返回空内容 / ctx 超时 / Generate 失败时返回 error，caller 退化为 LTRIM。
func (c *LLMCompactor) Compact(ctx context.Context, oldEntries []json.RawMessage) ([]byte, error) {
	if len(oldEntries) == 0 {
		return nil, fmt.Errorf("Compact: oldEntries 为空")
	}

	var b strings.Builder
	parsed := 0
	for i, raw := range oldEntries {
		var entry struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil || entry.Content == "" {
			continue
		}
		fmt.Fprintf(&b, "%d. %s\n", i+1, entry.Content)
		parsed++
	}
	if parsed == 0 {
		return nil, fmt.Errorf("Compact: oldEntries 全部无法解析")
	}

	result, err := c.gen.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: compactorSystemPrompt},
		{Role: llm.RoleUser, Content: b.String()},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("Compact: LLM Generate 失败: %w", err)
	}

	summary := strings.TrimSpace(result.Content)
	if summary == "" {
		return nil, fmt.Errorf("Compact: LLM 返回空摘要")
	}

	return json.Marshal(map[string]string{
		"content":   "[蒸馏摘要] " + summary,
		"hunter_id": compactorHunterID,
	})
}
