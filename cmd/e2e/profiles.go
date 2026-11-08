package main

import (
	"fmt"
	"sort"
	"strings"
)

// profile 描述一个 e2e 验收剧本：自然语言任务简报 + 验收门槛。
//
// brief 自然语言（含目标 URL/凭据/测试方向）整段 POST /chat 喂给 agent LLM，
// 由 LLM 自行识别 + 自主扫描；验收以探索图为断言（objective → action → result）。
type profile struct {
	name  string
	brief string

	// 验收标准：图谱节点数量门槛（验证完整认知循环）
	acceptance AcceptanceCriteria
}

// profiles 是 e2e 验收剧本集，CLI 参数直接写剧本名选择（如 xss / full）。
//
// - xss: 单一漏洞类型专项扫描
// - full: 综合开放性扫描
var profiles = map[string]profile{
	// full 综合扫描：开放性 brief，不剧透漏洞类型/路径。
	// 压测 LLM 自主 recon 能力 + 多攻击面 spawn 决策。
	//
	// - MinObjectives=1: 单根 objective（onboard 幂等去重后每任务 1 个根目标；
	//   次级目标由 Result 派生——0 result 时 1 即诚实上限，阈值 2 是双根 bug 时代的失真标定）
	// - MinActions=10: 综合扫描需要更多探索动作（recon + 多种利用方式）
	// - MinResults=3: 2026-10-02 按实测重标（LLM 裁决晋升链全通后单轮实产 4 result；
	//   5 是晋升链断裂时代的拍脑袋值——refute 率高是质量闸门正常工作，不该用高阈值逼迫放水）
	"full": {
		name:  "full",
		brief: "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password。**登录后立刻设 cookie `security=low`**（DVWA 默认 impossible 是修复版本，挖不到洞）。挖出尽可能多的漏洞，无类型限制。",
		acceptance: AcceptanceCriteria{
			MinObjectives: 1,
			MinActions:    10,
			MinResults:    3,
		},
	},

	// xss XSS 漏洞专项：登录 → 设 security=low → 扫 XSS（reflected/stored/DOM）。
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

// selectProfiles 解析 CLI args（剧本名，大小写不敏感），按参数顺序去重返回。
// 无参数 = 按字典序选中全部剧本（scripts/dev/e2e.sh 不带参即跑全套）。
// 未知剧本立即报错并列出可选项，避免静默忽略。
func selectProfiles(args []string) ([]profile, error) {
	if len(args) == 0 {
		names := knownProfileNames()
		all := make([]profile, len(names))
		for i, n := range names {
			all[i] = profiles[n]
		}
		return all, nil
	}

	selected := make([]profile, 0, len(args))
	seen := map[string]struct{}{}
	for _, a := range args {
		key := strings.ToLower(strings.TrimSpace(a))
		if key == "" {
			continue
		}
		p, ok := profiles[key]
		if !ok {
			return nil, fmt.Errorf("未知 profile %q（可选: %s）", key, strings.Join(knownProfileNames(), ", "))
		}
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			selected = append(selected, p)
		}
	}
	return selected, nil
}

// knownProfileNames 返回全部剧本名（字典序）。
func knownProfileNames() []string {
	names := make([]string, 0, len(profiles))
	for k := range profiles {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
