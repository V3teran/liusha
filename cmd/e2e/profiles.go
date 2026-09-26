package main

import (
	"fmt"
	"sort"
	"strings"
)

// activeProfile 描述一个 active 模式 e2e 验收剧本：自然语言任务简报 + 验收门槛。
//
// brief 自然语言（含目标 URL/凭据/测试方向）整段 POST /chat 喂给 agent LLM，
// 由 LLM 自行识别 + 自主扫描；验收以知识图谱为断言（objective → action → result）。
type activeProfile struct {
	name  string
	brief string

	// 验收标准：图谱节点数量门槛（验证完整认知循环）
	acceptance AcceptanceCriteria
}

// activeProfiles 是 active 模式 e2e 验收剧本集，args 用前缀 active:<name> 选择。
//
// - xss: 单一漏洞类型专项扫描
// - full: 综合开放性扫描
var activeProfiles = map[string]activeProfile{
	// active:full 综合扫描：开放性 brief，不剧透漏洞类型/路径。
	// 压测 LLM 自主 recon 能力 + swarm spawn 决策（多攻击面应触发 spawn_exploitation）。
	//
	// - MinObjectives=2: 可能有多个攻击面目标（登录、XSS、SQLi、文件上传等）
	// - MinActions=10: 综合扫描需要更多探索动作（recon + 多种攻击）
	// - MinResults=5: 期望发现多个漏洞
	"full": {
		name:  "full",
		brief: "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password。挖出尽可能多的漏洞，无类型限制。",
		acceptance: AcceptanceCriteria{
			MinObjectives: 2,
			MinActions:    10,
			MinResults:    5,
		},
	},

	// active:xss XSS 漏洞专项：登录 → 设 security=low → 扫 XSS（reflected/stored/DOM）。
	//
	// - MinObjectives=1: "扫描 XSS 漏洞"这个目标
	// - MinActions=5: 至少执行 5 个动作（登录、设cookie、扫描表单、测试注入点、验证漏洞）
	// - MinResults=3: 至少发现 3 个确认的 XSS 漏洞
	"xss": {
		name:  "xss",
		brief: "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password。**登录后立刻设 cookie `security=low`**（DVWA 默认 impossible 是修复版本，挖不到洞）。专注挖 XSS 漏洞。",
		acceptance: AcceptanceCriteria{
			MinObjectives: 1,
			MinActions:    5,
			MinResults:    3,
		},
	},
}

// resolveActiveProfile 解析 active:<name> 形式的 arg，返回对应 profile。
// 不存在时报错列出可选项。
func resolveActiveProfile(arg string) (activeProfile, error) {
	name := strings.TrimPrefix(arg, "active:")
	if p, ok := activeProfiles[name]; ok {
		return p, nil
	}
	available := make([]string, 0, len(activeProfiles))
	for k := range activeProfiles {
		available = append(available, k)
	}
	sort.Strings(available)
	return activeProfile{}, fmt.Errorf("未知 active profile %q，可选：%v", name, available)
}

// selectProfiles 解析 CLI args，选出 active profiles。
//
// args 前缀语义："active:<name>" → 选 activeProfiles[name]。
// 未知 profile 立即报错，避免静默忽略。
func selectProfiles(args []string) ([]activeProfile, error) {
	active := make([]activeProfile, 0, len(args))
	seenActive := map[string]struct{}{}

	for _, a := range args {
		raw := strings.ToLower(strings.TrimSpace(a))
		if raw == "" {
			continue
		}
		if !strings.HasPrefix(raw, "active:") {
			return nil, fmt.Errorf("未知 profile %q（仅支持 active:<name>）", raw)
		}
		key := strings.TrimPrefix(raw, "active:")
		ap, ok := activeProfiles[key]
		if !ok {
			known := make([]string, 0, len(activeProfiles))
			for k := range activeProfiles {
				known = append(known, k)
			}
			sort.Strings(known)
			return nil, fmt.Errorf("未知 active profile %q（可选: %s）", key, strings.Join(known, ", "))
		}
		if _, dup := seenActive[key]; !dup {
			seenActive[key] = struct{}{}
			active = append(active, ap)
		}
	}
	return active, nil
}
