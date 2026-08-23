// Package web 是第一个 Domain Profile 实现（L2）：把自由文本 brief 里的 http(s) 目标
// 解析成 web 域的 TargetRef。它证明架构试金石——加一个域只需实现接口 + 注册，核心零改动。
// 二进制/云/lateral 域后续按同样方式各出一个包。
package web

import (
	"context"
	"fmt"
	"regexp"

	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// hostRe 匹配 http(s):// 后到 / 或空白之前的 host（含端口）。
// 从 cmd/runner/main.go 的 briefHostRe 原样迁入——host 抽取归 web 域，核心不再持有正则。
// 捕获组 [1] 即 host[:port]，与旧 target_host / 归档切分键逐字一致（保行为）。
var hostRe = regexp.MustCompile(`https?://([^/\s]+)`)

// Profile 实现 executor.Profile，域标识 "web"：把 brief 解析成 web 目标（host[:port]）。
type Profile struct{}

// New 构造 web Profile。工具/skills 不经 Profile（走 Executor.cli_tools + skill 目录）。
func New() *Profile {
	return &Profile{}
}

func (p *Profile) Domain() string { return "web" }

// Onboard 把自由文本 brief 解析成 web 目标。取代 cmd/runner/main.go 的 briefHostRe——
// 解析逻辑归 Profile，核心不再抽 host。
//
// Locator = host[:port]（非整条 URL）：目标粒度是"站点/scope"，endpoint 是后续爬取/
// 流量发现的 asset。host key 与旧 target_host / 归档切分键逐字一致（保行为）。去重保序。
func (p *Profile) Onboard(_ context.Context, in executor.BriefInput) ([]worldmodel.TargetRef, error) {
	hosts := extractHosts(in.Brief)
	if len(hosts) == 0 {
		return nil, fmt.Errorf("web.Onboard: brief 中未发现 http(s) 目标")
	}
	refs := make([]worldmodel.TargetRef, 0, len(hosts))
	for _, h := range hosts {
		refs = append(refs, worldmodel.TargetRef{
			Domain:  "web",
			RefKind: "host",
			Locator: h,
		})
	}
	return refs, nil
}

// extractHosts 从自由文本抽取 http(s) 目标的 host[:port]。web 域私有逻辑，不外泄到核心。
// 用 hostRe（原 briefHostRe）全局扫描，保序去重——支持一段 brief 含多个目标。
func extractHosts(brief string) []string {
	matches := hostRe.FindAllStringSubmatch(brief, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		host := m[1]
		if _, dup := seen[host]; dup {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	return out
}

// 编译期断言：Profile 满足 executor.Profile 接口。
var _ executor.Profile = (*Profile)(nil)
