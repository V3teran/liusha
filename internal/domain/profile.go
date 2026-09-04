// Package executor 定义 L2 域适配层的 Domain Profile 接口。
//
// 一个 Profile 负责一个域（web/binary/cloud/lateral）的目标接入：把用户 brief 解析成
// 该域的多态目标（TargetRef）。核心对 Profile 只认接口、零硬编——加新域 = 实现一个
// Profile 并注册，核心零改动（这是架构试金石）。
//
// 工具集/skills/finding 形状不走 Profile：工具真相源是 Executor.cli_tools（严格白名单
// 过滤 tools.yaml manifest），skills 走 internal/skill 目录加载——都与域正交（能力轴，
// 领域中立），Profile 不做工具/技能的域维度过滤。
//
// 迁移史铁律：domain 概念只活在 Profile 实现里，永不进 DB 当过滤维度
// （.domain 删过 0094、tool.s 删过 0093，不许第三次复活）。
package domain

import (
	"context"

	"github.com/V3teran/liusha/internal/worldmodel"
)

// BriefInput 是用户下发的原始目标描述（自由文本 + 可选结构化线索）。
// 取代原 cmd/runner 的 briefHostRe 正则——核心不再抽 host，host 抽取归各域 Profile。
type BriefInput struct {
	Brief string            // 自由文本任务说明
	Hints map[string]string // 可选结构化线索（如显式 scope、区域）
}

// Profile 是一个域的完整适配契约。
type Profile interface {
	// Domain 返回域标识（web|binary|cloud|lateral）。
	Domain() string

	// Onboard 解析用户输入 → 多态目标（取代 briefHostRe）。
	Onboard(ctx context.Context, in BriefInput) ([]worldmodel.TargetRef, error)
}

// Registry 是 Profile 的注册表。核心通过 domain 字符串查 Profile，
// 不做任何 switch-on-domain 的硬编码分支。
type Registry struct {
	byDomain map[string]Profile
}

// NewRegistry 构造空注册表。
func NewRegistry() *Registry {
	return &Registry{byDomain: make(map[string]Profile)}
}

// Register 注册一个域的 Profile。重复注册同一域会覆盖（后注册者胜）。
func (r *Registry) Register(p Profile) {
	r.byDomain[p.Domain()] = p
}

// Get 按域标识取 Profile；未注册返回 (nil, false)。
func (r *Registry) Get(domain string) (Profile, bool) {
	p, ok := r.byDomain[domain]
	return p, ok
}

// Domains 返回已注册的全部域标识。
func (r *Registry) Domains() []string {
	out := make([]string, 0, len(r.byDomain))
	for d := range r.byDomain {
		out = append(out, d)
	}
	return out
}

// Onboard 聚合所有已注册 Profile 的目标解析：每个域自认领它能解析的目标
// （web 认 http(s)、binary 认文件路径……），核心不做任何 switch-on-domain。
// 单个 Profile 无匹配时返回错误（无目标），聚合层视为该域弃权、静默跳过；
// 只要有一个域认领到目标即成功。全域皆无目标才返回 (nil, false)。
//
// 顺序由 domains 决定（map 无序）——调用方若需稳定序应自行按 ref 排序。
func (r *Registry) Onboard(ctx context.Context, in BriefInput) ([]worldmodel.TargetRef, bool) {
	var all []worldmodel.TargetRef
	for _, p := range r.byDomain {
		refs, err := p.Onboard(ctx, in)
		if err != nil {
			continue // 该域弃权（brief 里没有它能解析的目标）
		}
		all = append(all, refs...)
	}
	return all, len(all) > 0
}
