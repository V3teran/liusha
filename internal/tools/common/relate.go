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
	return "显式声明 finding A 是 finding B 的前提（enables 关系，组合漏洞推理）。" +
		"**何时用**：发现两条 finding 间有依赖（A 不存在时 B 无法复现），" +
		"如 SQLi 拿到 admin cookie 后才能测出某 BAC。" +
		"from/to 都是 finding ID（先用 findings() 查 ID）；reason 自由文本说明依赖逻辑。" +
		"渲染到 graph viewer 的 enables 边，方便看组合攻击链。"
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
