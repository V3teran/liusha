package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/subtask"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// SpawnStriker 是 subtask swarm 的派单工具——只commander注册。
//
// hunter builder 在 CommanderID == "" 路径注册本工具 + ListStrikers；
// striker（CommanderID 非空）不注册，强制 max_depth=1。
//
// max_children 闸值由 Spawner 持有；闸触发的 wrapped error 已含数字提示，直接透传给 LLM。
type SpawnStriker struct {
	Spawner subtask.Spawner
}

// Name 返回工具名 "spawn_striker"。
func (a SpawnStriker) Name() string { return "spawn_striker" }

func (a SpawnStriker) Description() string {
	return "派一个 striker 并行深挖某个独立攻击面（仅 commander 可调）。立即返回 {\"striker_id\": ...}（异步），" +
		"commander 继续做别的；striker 的 finding 自动通过共享黑板（read_findings）冒给 commander——**不要 polling list_strikers**。" +
		"\n\n【何时调】recon 阶段发现 ≥ 2 个独立 endpoint/feature；正在挖 X 时临时发现 Y；站点 N 个业务面（admin/user/api）。" +
		"\n【何时不调】单一 endpoint 深挖（顺序依赖）；recon 还没跑完盲目派；已 spawn 接近上限。" +
		"\n【brief 写作】≤1000 字自然语言，目标范围 + 关键背景。striker 继承本 host，不需重复站点 URL；" +
		"striker 能读本 host 的 note/lesson/finding（黑板共享），无需复制 context。" +
		"\n【done 约束】所有 striker 完成你才能 done——直接调 done 即可，被拦后 PreDoneCheck 返结构化错误（含 running striker 摘要 + 行动建议如 read_findings / 挖新链路 / write_lesson）；按错误消息执行，**不要 retry done 或 polling list_strikers**。"
}

// ParametersJSON 给出 brief 必填 + flow_id 可选 schema（maxLength 1000）。
func (a SpawnStriker) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "brief":{"type":"string","minLength":10,"maxLength":1000,"description":"striker 自然语言描述，如 '深挖 /admin 后台的权限绕过 + 后台功能 XSS，已知 admin/password 可登录'"},
    "flow_id":{"type":"integer","description":"可选——tracker 传自己的 flow_id，striker 能在 user prompt 看到完整 raw HTTP 请求+响应（最高信息密度）。commander 无 flow 留空即可，striker 仅看 brief。"}
  },
  "required":["brief"]
}`)
}

// Execute 调 Spawner.Spawn，max_children 闸触发时返回带数字的友好错。
func (a SpawnStriker) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	if a.Spawner == nil {
		return toolfx.Result{}, errors.New("spawn_striker: Spawner 未注入")
	}
	var in struct {
		Brief  string `json:"brief"`
		FlowID int64  `json:"flow_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 spawn_striker 参数失败: %w", err)
	}
	if in.Brief == "" {
		return toolfx.Result{}, errors.New("brief 必填")
	}

	childID, err := a.Spawner.Spawn(ctx, in.Brief, subtask.SpawnOptions{FlowID: in.FlowID})
	if err != nil {
		// max_children 闸触发时 Spawner 返带数字的 wrapped error，直接透传；
		// 其他错误也透传——LLM 自行决策重试/换策略。
		return toolfx.Result{}, fmt.Errorf("spawn_striker: %w", err)
	}

	out, _ := json.Marshal(map[string]string{"striker_id": childID})
	summary := fmt.Sprintf("spawn_striker striker_id=%s", childID)
	return toolfx.Result{Output: out, Summary: summary}, nil
}
