package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// RelationStore 是 WriteRelation 工具依赖的最小接口。
// *finding.Store 自动满足。
type RelationStore interface {
	SaveRelation(ctx context.Context, fromID, toID, reason string) (finding.Relation, error)
}

// WriteRelation — 显式声明两条 finding 之间的 enables 关系（组合漏洞依赖）。
//
// v0024 agentic：让 LLM 主动控图——发现「finding A 是 finding B 的前提」时调本工具，
// 写一条 enables 边到 finding_relation 表，graph projector 渲染成图边。
type WriteRelation struct {
	Store RelationStore
}

// Name 返回工具名 "write_relation"。
func (a *WriteRelation) Name() string { return "write_relation" }

// Description 提供给 LLM 的简介。
func (a *WriteRelation) Description() string {
	return "显式声明 finding A 是 finding B 的前提（enables 关系，**跨流量组合漏洞**——1+1≥2）。" +
		"**何时用**：当前流量挖到的漏洞 + read_findings 看到的他人流量历史 finding，" +
		"两者组合能放大危害（A 单独存在不致命、B 单独存在低危，但 A→B 链能拿系统/数据）。" +
		"典型：流量 X 暴露 admin cookie（A）+ 流量 Y 的越权接口（B）→ 用 A 的 cookie 走 B 接口拿全数据。" +
		"from/to 都是 finding ID（read_findings 拿）；reason 自由文本写为何 A 是 B 的前提。" +
		"**单流量内同一漏洞的多个 finding 不要 relate**——那是 dedup 问题不是组合。"
}

// ParametersJSON 给出 from/to 必填 + reason 可选 schema。
func (a *WriteRelation) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "from":{"type":"string","description":"前提 finding 的 id（先调 findings() 查）"},
    "to":{"type":"string","description":"被依赖 finding 的 id（A 是 B 的前提）"},
    "reason":{"type":"string","description":"自由文本说明为什么 A 是 B 的前提"}
  },
  "required":["from","to"]
}`)
}

// Execute 解析参数 → 调 Store.SaveRelation → 返回 relation_id。
func (a *WriteRelation) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	if a.Store == nil {
		return toolfx.Result{}, errors.New("write_relation: Store nil")
	}

	var in struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 write_relation 参数失败: %w", err)
	}
	if in.From == "" || in.To == "" {
		return toolfx.Result{}, errors.New("from / to 都必填（finding id）")
	}
	if in.From == in.To {
		return toolfx.Result{}, errors.New("from 与 to 不能相同")
	}

	saved, err := a.Store.SaveRelation(ctx, in.From, in.To, in.Reason)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("保存 relation 失败: %w", err)
	}

	out, _ := json.Marshal(map[string]string{"id": saved.ID})
	return toolfx.Result{Output: out}, nil
}
