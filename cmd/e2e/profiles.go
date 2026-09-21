package main

import (
	"fmt"
	"sort"
	"strings"
)

// credentialEntry 是 /credential/batch 单条身份记录的结构。
type credentialEntry struct {
	Name        string              `json:"name"`
	Role        string              `json:"role"`
	Credentials []map[string]string `json:"credentials"`
}

// profile 描述一个 passive 模式 e2e 验收剧本（BAC / SQLi）：sample 文件 + 身份集 + 验收门槛。
//
// Phase 2 重构：验收标准从 minFindings 改为 AcceptanceCriteria（验证完整认知循环）。
// 旧标准盲区：只看 finding 数量，LLM 可能"猜"对但没有推理链。
// 新标准优势：验证 objective → action → observation → evaluation → result 完整流程。
type profile struct {
	name           string
	defaultSamples string

	// Phase 2: 新验收标准（优先使用）
	acceptance AcceptanceCriteria

	// Phase 1: 旧验收标准（向后兼容，逐步迁移）
	minFindings int

	// credsForHost 接收样本所属 host 返回该 host 的身份列表（profile 自决定身份组）。
	credsForHost func(host string) []credentialEntry
}

// activeProfile 描述一个 active 模式 e2e 验收剧本：自然语言任务简报 + 验收门槛。
//
// 与 passive 的 profile 不同——active 没有 sample 流量文件，直接把 brief 自然
// 语言（含目标 URL/凭据/测试方向）整段 POST /chat 喂给 agent LLM，由
// LLM 自行识别 + 自主扫描。
type activeProfile struct {
	name  string
	brief string

	// Phase 2: 新验收标准（优先使用）
	acceptance AcceptanceCriteria

	// Phase 1: 旧验收标准（向后兼容，逐步迁移）
	minFindings int
}

// useNewAcceptance 判断是否使用新验收标准
func (p profile) useNewAcceptance() bool {
	// 如果 acceptance 有任何非零值，说明已配置新标准
	return p.acceptance.MinObjectives > 0 ||
		   p.acceptance.MinActions > 0 ||
		   p.acceptance.MinResults > 0
}

// useNewAcceptance 判断是否使用新验收标准
func (ap activeProfile) useNewAcceptance() bool {
	return ap.acceptance.MinObjectives > 0 ||
		   ap.acceptance.MinActions > 0 ||
		   ap.acceptance.MinResults > 0
}

// activeProfiles 是 active 模式 e2e 验收剧本集，args 用前缀 active:<name> 选择。
//
// Phase 2 精简：只保留 2 个最核心的 active profiles
// - xss: 单一漏洞类型专项扫描（已更新新标准）
// - full: 综合开放性扫描（需要更新新标准）
var activeProfiles = map[string]activeProfile{
	// active:full 综合扫描：开放性 brief，不剧透漏洞类型/路径。
	// 压测 LLM 自主 recon 能力 + swarm spawn 决策（多攻击面应触发 spawn_exploitation）。
	//
	// Phase 2 新标准：验证完整认知循环
	// - MinObjectives=2: 可能有多个攻击面目标（登录、XSS、SQLi、文件上传等）
	// - MinActions=10: 综合扫描需要更多探索动作（recon + 多种攻击）
	// - MinResults=5: 期望发现多个漏洞（minFindings=8 的降级版，留余量）
	"full": {
		name:  "full",
		brief: "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password。挖出尽可能多的漏洞，无类型限制。",
		acceptance: AcceptanceCriteria{
			MinObjectives: 2,
			MinActions:    10,
			MinResults:    5,
		},
		minFindings: 8, // 向后兼容（旧标准）
	},

	// active:xss XSS 漏洞专项：登录 → 设 security=low → 扫 XSS（reflected/stored/DOM）。
	//
	// Phase 2 新标准：验证完整认知循环
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
		minFindings: 3, // 向后兼容（旧标准）
	},
}

// profiles 是 passive 模式 e2e 验收剧本集，args 用逗号分隔选择（如 "bac,sqli"）。
//
// Phase 2 精简：暂时注释掉所有 passive profiles（以后需要时恢复）
// passive 模式涉及代理、流量发送、聚合器等复杂逻辑，需要单独实现知识图谱轮询
var profiles = map[string]profile{
	// TODO: Phase 2.1 - 实现 passive 模式的知识图谱轮询后恢复
	// "sqli": {
	// 	name:           "sqli",
	// 	defaultSamples: "examples/sample_sqli_raw.json",
	// 	acceptance: AcceptanceCriteria{
	// 		MinObjectives: 1,
	// 		MinActions:    3,
	// 		MinResults:    1,
	// 	},
	// 	minFindings: 1,
	// 	credsForHost: func(_ string) []credentialEntry {
	// 		return []credentialEntry{
	// 			{Name: "admin", Role: "admin", Credentials: []map[string]string{
	// 				{"type": "headers", "key": "Cookie", "value": "PHPSESSID=sqli_admin_sess_xyz"},
	// 			}},
	// 		}
	// 	},
	// },
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

// resolvePassiveProfiles 解析逗号分隔的 profile names（如 "bac,sqli"）。
// 空字符串 → 全部 profile；"none" → 空列表；否则按名字选择。
func resolvePassiveProfiles(arg string) ([]profile, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		// 默认：全部 passive profiles
		all := make([]profile, 0, len(profiles))
		for _, p := range profiles {
			all = append(all, p)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].name < all[j].name })
		return all, nil
	}
	if arg == "none" {
		return nil, nil
	}

	names := strings.Split(arg, ",")
	selected := make([]profile, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		p, ok := profiles[name]
		if !ok {
			available := make([]string, 0, len(profiles))
			for k := range profiles {
				available = append(available, k)
			}
			sort.Strings(available)
			return nil, fmt.Errorf("未知 passive profile %q，可选：%v", name, available)
		}
		selected = append(selected, p)
	}
	return selected, nil
}

// selectProfiles 解析 CLI args 拆成 (passive, active) 两组。
//
// args 前缀语义：
//   - "active:<name>"            → 选 activeProfiles[name]
//   - "passive:<name>[,<name>…]" → 逗号分隔多选 profiles（如 passive:upload,lfi）
//   - 裸名                       → 选 profiles[name]（passive，空格分隔多选，向后兼容）
//
// 空 args = 跑全部 passive profile（active 必须显式 `active:xxx` 选，避免无意中
// 触发耗资源的真实站点扫描）。未知 profile 立即报错，避免静默忽略。
func selectProfiles(args []string) ([]profile, []activeProfile, error) {
	if len(args) == 0 {
		all := make([]profile, 0, len(profiles))
		for _, p := range profiles {
			all = append(all, p)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].name < all[j].name })
		return all, nil, nil
	}
	passive := make([]profile, 0, len(args))
	active := make([]activeProfile, 0, len(args))
	seenPassive := map[string]struct{}{}
	seenActive := map[string]struct{}{}

	// addPassive 是所有 passive 选择路径（裸名 + passive: 前缀逗号项）的统一入口：
	// 查表 + 去重 + 追加，未知名立即报错。
	addPassive := func(name string) error {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil
		}
		p, ok := profiles[name]
		if !ok {
			known := make([]string, 0, len(profiles))
			for k := range profiles {
				known = append(known, k)
			}
			sort.Strings(known)
			return fmt.Errorf("未知 profile %q（可选: %s 或 active:<name>）", name, strings.Join(known, ", "))
		}
		if _, dup := seenPassive[name]; dup {
			return nil
		}
		seenPassive[name] = struct{}{}
		passive = append(passive, p)
		return nil
	}

	for _, a := range args {
		raw := strings.ToLower(strings.TrimSpace(a))
		if raw == "" {
			continue
		}
		switch {
		case strings.HasPrefix(raw, "active:"):
			key := strings.TrimPrefix(raw, "active:")
			ap, ok := activeProfiles[key]
			if !ok {
				known := make([]string, 0, len(activeProfiles))
				for k := range activeProfiles {
					known = append(known, k)
				}
				sort.Strings(known)
				return nil, nil, fmt.Errorf("未知 active profile %q（可选: %s）", key, strings.Join(known, ", "))
			}
			if _, dup := seenActive[key]; !dup {
				seenActive[key] = struct{}{}
				active = append(active, ap)
			}
		case strings.HasPrefix(raw, "passive:"):
			names := strings.TrimPrefix(raw, "passive:")
			for _, n := range strings.Split(names, ",") {
				if err := addPassive(n); err != nil {
					return nil, nil, err
				}
			}
		default:
			if err := addPassive(raw); err != nil {
				return nil, nil, err
			}
		}
	}
	return passive, active, nil
}
