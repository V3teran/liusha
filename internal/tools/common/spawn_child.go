package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/subtask"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// SpawnChild 是 subtask swarm 的派单工具——只 active 父任务注册。
//
// hunter builder 在 (Mode == "active" && ParentTaskID == "") 路径注册本工具
// + ListChildren；子任务（ParentTaskID 非空）不注册，强制 max_depth=1。
type SpawnChild struct {
	Spawner     subtask.Spawner
	MaxChildren int // 仅用于错误消息提示 LLM 实际闸值；真正的闸由 Spawner 内部检查
}

// Name 返回工具名 "spawn_child"。
func (a SpawnChild) Name() string { return "spawn_child" }

// Description 提供给 LLM 的简介。
func (a SpawnChild) Description() string {
	return "派一个子 active hunter 并行深挖某个独立攻击面。立即返回 child_task_id（异步），" +
		"父继续做别的；调 list_children 看子进度，子的 finding 自动通过共享黑板（read_findings）冒给父。" +
		"\n\n【何时调】recon 阶段发现 ≥ 2 个独立 endpoint/feature；正在挖 X 时临时发现 Y；站点 N 个业务面（admin/user/api）。" +
		"\n【何时不调】单一 endpoint 深挖（顺序依赖）；recon 还没跑完盲目派；已 spawn 接近上限。" +
		"\n【brief 写作】≤1000 字自然语言，目标范围 + 关键背景。子继承本 host，不需重复站点 URL；" +
		"子能读本 host 的 note/lesson/finding（黑板共享），无需复制 context。" +
		"\n【done 约束】所有子完成你才能 done — 否则 done 工具会拒绝（你需先 list_children 等子完成）。"
}

// ParametersJSON 给出 brief 必填 + flow_id 可选 schema（maxLength 1000）。
func (a SpawnChild) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "brief":{"type":"string","minLength":10,"maxLength":1000,"description":"子任务自然语言描述，如 '深挖 /admin 后台的权限绕过 + 后台功能 XSS，已知 admin/password 可登录'"},
    "flow_id":{"type":"integer","description":"可选——父 passive 任务传自己的 flow_id，子能在 user prompt 看到完整 raw HTTP 请求+响应（最高信息密度）。active 父无 flow 留空即可，子仅看 brief。"}
  },
  "required":["brief"]
}`)
}

// Execute 调 Spawner.Spawn，max_children 闸触发时返回带数字的友好错。
func (a SpawnChild) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	if a.Spawner == nil {
		return toolfx.Result{}, errors.New("spawn_child: Spawner 未注入")
	}
	var in struct {
		Brief  string `json:"brief"`
		FlowID int64  `json:"flow_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 spawn_child 参数失败: %w", err)
	}
	if in.Brief == "" {
		return toolfx.Result{}, errors.New("brief 必填")
	}

	childTID, err := a.Spawner.Spawn(ctx, in.Brief, subtask.SpawnOptions{FlowID: in.FlowID})
	if errors.Is(err, subtask.ErrMaxChildren) {
		return toolfx.Result{}, fmt.Errorf("已达 max_children=%d 上限，调 list_children 看子进度，等部分子完成再 spawn", a.MaxChildren)
	}
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("spawn_child: %w", err)
	}

	out, _ := json.Marshal(map[string]string{"child_task_id": childTID})
	summary := fmt.Sprintf("spawn_child task_id=%s", childTID)
	return toolfx.Result{Output: out, Summary: summary}, nil
}
