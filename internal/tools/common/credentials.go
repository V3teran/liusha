package common

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/toolruntime"
)

// ReadCredentials — 拉取本 task 目标 host 的全部活身份（含 cookie/token raw value）。
//
// v0024 agentic-lean：替代旧 fetch_credentials 工具——
//   - 删除 roles/names 过滤（LLM 自己看返回结果筛选）
//   - 删除 anonymous 自动注入（LLM 想测匿名访问就用占位 token "lstoken" 替换 cookie 值跑 curl）
//   - host 由 builder 注入（per-task 绑定），LLM 不传参
type ReadCredentials struct {
	Provider credential.Provider
	Host     string // builder 注入；空时 Execute 报错
}

// Name 返回工具名 "read_credentials"。
func (a *ReadCredentials) Name() string { return "read_credentials" }

// Description 提供给 LLM 的简介。
func (a *ReadCredentials) Description() string {
	return "拉取本 task 目标 host 的全部活身份（含 cookie/token/body 字段值，可直接拼请求）。" +
		"**何时用**：流量请求带 cookie/token/Authorization 等认证字段，且你想用其他身份重放（测越权、" +
		"复用 admin 看完整数据）。公开接口（请求无任何认证字段）不需要调用。" +
		"返回 [{name, role, credentials: [{type, key, value}]}] —— type ∈ headers/query/body。" +
		"测匿名/未授权访问：用占位 token 'lstoken' 替换 cookie 值跑 curl，能精确触发服务端" +
		"『token 校验失败』分支（vs 完全无 cookie 走『未登录』分支，可能漏掉真实认证缺陷）。"
}

// ParametersJSON 无入参（host 由 builder 注入）。
func (a *ReadCredentials) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// Execute 拉 host 全部身份返回 JSON。
func (a *ReadCredentials) Execute(ctx context.Context, _ json.RawMessage) (toolfx.Result, error) {
	if a.Host == "" {
		return toolfx.Result{}, fmt.Errorf("credentials: Host 必填（builder 注入失败）")
	}
	if a.Provider == nil {
		return toolfx.Result{}, fmt.Errorf("credentials: Provider nil")
	}

	identities, err := a.Provider.GetIdentitiesByHost(ctx, a.Host)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("拉取 %s 凭证失败: %w", a.Host, err)
	}

	out, err := json.Marshal(identities)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal credentials: %w", err)
	}
	return toolfx.Result{Output: out}, nil
}
