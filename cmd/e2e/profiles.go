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
// 验收门槛极简——minFindings 是 LLM 能力回归底线：本 profile dispatch 后只要本
// owner 至少多出 minFindings 条 finding 就算 PASS。LLM 输出本就有抖动，
// "挖出 1 条 vs 3 条"不是稳定指标；唯一要捕捉的退化场景是"原本能挖出现在
// 完全挖不到"，minFindings=1 就够覆盖。severity 等级分布不再参与判定。
type profile struct {
	name           string
	defaultSamples string
	minFindings    int
	// credsForHost 接收样本所属 host 返回该 host 的身份列表（profile 自决定身份组）。
	credsForHost func(host string) []credentialEntry
}

// activeProfile 描述一个 active 模式 e2e 验收剧本：自然语言任务简报 + 验收门槛。
//
// 与 passive 的 profile 不同——active 没有 sample 流量文件，直接把 brief 自然
// 语言（含目标 URL/凭据/测试方向）整段 POST /scan/active 喂给 hunter LLM，由
// LLM 自行识别 + 自主扫描。
type activeProfile struct {
	name        string
	brief       string
	minFindings int
}

// activeProfiles 是 active 模式 e2e 验收剧本集，args 用前缀 active:<name> 选择。
//
// 当前只内置 1 个 demo (xss)——用户实际用 active 模式时，按需追加新 profile 即可。
// 开放性 brief——只给入口 + 凭证，不剧透漏洞类型 / 独立漏洞页面。
// 压测 LLM 自主 recon 能力 + swarm spawn 决策（多攻击面应触发 spawn_exploitation）。
// minFindings=8 防 LLM 拿少量 finding 就 done，逼它走完 spawn 路径。
var activeProfiles = map[string]activeProfile{
	"full": {
		name:        "full",
		brief:       "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password。挖出尽可能多的漏洞，无类型限制。",
		minFindings: 8,
	},
	// active:xss 极简 brief：不剧透 endpoint 路径，不剧透 DOM XSS 需 browser，
	// 完全由 LLM 自主 recon + 自决工具（curl vs page_*）。压测真自主能力。
	"xss": {
		name:        "xss",
		brief:       "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password。**登录后立刻设 cookie `security=low`**（DVWA 默认 impossible 是修复版本，挖不到洞）。专注挖 XSS 漏洞。",
		minFindings: 3,
	},
	// active:privesc 垂直越权：给两组凭据（admin / 普通用户 gordonb），压测 LLM 用低权
	// 身份访问 admin-only 资源的越权判定。单一漏洞类型，minFindings=1 取回归底线（能挖到即可）。
	"privesc": {
		name:        "privesc",
		brief:       "测试网站 http://111.229.193.40:34280/login.php，管理员是admin/password，普通用户是gordonb/abc123。**登录后立刻设 cookie `security=low`**（DVWA 默认 impossible 是修复版本，挖不到洞）。只挖掘垂直越权漏洞。",
		minFindings: 1,
	},
	// active:bac 全 BAC：三种形态（未授权 / 垂直 / 水平）全覆盖，比 privesc 宽。给两组凭据
	// （admin / gordonb）压测多身份重放对比 + anonymous 探测。minFindings=1 取回归底线（能挖到即可）。
	"bac": {
		name:        "bac",
		brief:       "测试网站 http://111.229.193.40:34280/login.php，管理员是admin/password，普通用户是gordonb/abc123。**登录后立刻设 cookie `security=low`**（DVWA 默认 impossible 是修复版本，挖不到洞）。只挖掘未授权访问、垂直越权、水平越权漏洞。",
		minFindings: 1,
	},
	// active:adhoc 一次性扫描——brief 留空，runner.go 强制从 LIUSHA_E2E_BRIEF 环境变量读取
	// （避免把含密码的 brief 写进 git）。minFindings 可被 LIUSHA_E2E_MIN_FINDINGS 覆盖（默认 1）。
	"adhoc": {
		name:        "adhoc",
		brief:       "",
		minFindings: 1,
	},
}

var profiles = map[string]profile{
	"bac": {
		name:           "bac",
		defaultSamples: "examples/sample_bac_raw.json",
		minFindings:    1,
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "session=admin_sess_a1b2c3"},
				}},
				{Name: "test", Role: "user", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "session=test_sess_d4e5f6"},
				}},
				{Name: "m233241", Role: "user", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "session=m233241_sess_g7h8i9"},
				}},
			}
		},
	},
	"sqli": {
		name:           "sqli",
		defaultSamples: "examples/sample_sqli_raw.json",
		minFindings:    1,
		// DVWA 远程靶场（111.229.193.40:34280）：仅 admin 身份。
		// PHPSESSID + security=low 双 cookie 拼成一行；旧 gordonb 身份的 cookie 在新靶机上无效，
		// 需要时让用户在远程 DVWA 重新登录拿 cookie 再补回来。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"xss": {
		name:           "xss",
		defaultSamples: "examples/sample_xss_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场，admin 同凭证；3 条样本覆盖 reflected (xss_r) / stored (xss_s) / DOM (xss_d) 三种场景。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"brute": {
		name:           "brute",
		defaultSamples: "examples/sample_brute_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/brute/ 是登录表单类暴力破解漏洞。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"lfi": {
		name:           "lfi",
		defaultSamples: "examples/sample_lfi_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/fi/?page= 是文件包含 / 路径遍历 / 任意文件读取漏洞（OWASP CWE-22）。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"upload": {
		name:           "upload",
		defaultSamples: "examples/sample_upload_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/upload/ 是 Unrestricted File Upload（OWASP CWE-434）。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"csrf": {
		name:           "csrf",
		defaultSamples: "examples/sample_csrf_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/csrf/ 是 CSRF（OWASP CWE-352）：
		// 关键特征是 sample 流量本身用 GET 改密码，缺少 anti-CSRF token——主漏洞证据
		// 已写在流量入口里。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"api": {
		name:           "api",
		defaultSamples: "examples/sample_api_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/api/ 是 API 类漏洞入口（具体漏洞类型由 hunter
		// agent 探测：可能是 IDOR / 信息泄露 / 弱认证 / 注入等）。Referer 来自 cryptography
		// 页面意味着这是从其他漏洞链路跳过来的 API 端点。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"cryptography": {
		name:           "cryptography",
		defaultSamples: "examples/sample_cryptography_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/cryptography/ 是密码学相关漏洞类（OWASP CWE-310/327）：
		// 弱加密算法 / 硬编码密钥 / 弱随机数 / IV 复用 / 弱哈希等。具体漏洞由 hunter agent
		// 通过 read_vuln_skill + 读源码 / 多请求差分等手段判定。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"redirect": {
		name:           "redirect",
		defaultSamples: "examples/sample_redirect_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/open_redirect/ 是开放重定向（OWASP CWE-601）：
		// URL 参数控制跳转目标但未做域白名单校验，可被钓鱼利用。hunter agent 通过构造
		// redirect=<外部域> 参数 + 看 Location header 是否原样返回判定。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"authbypass": {
		name:           "authbypass",
		defaultSamples: "examples/sample_authbypass_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/authbypass/ 是认证绕过类（OWASP CWE-287/863）：
		// 鉴权逻辑缺陷可直接越过登录访问受保护资源。hunter agent 通过 anonymous /
		// 修改 cookie / Header 篡改等多手法判定。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"csp": {
		name:           "csp",
		defaultSamples: "examples/sample_csp_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/csp/ 是 Content-Security-Policy 配置问题
		// （OWASP CWE-1021）：过宽 CSP（含 unsafe-inline / unsafe-eval / 通配符 source）
		// 削弱 XSS 防护。hunter agent 通过读 Content-Security-Policy 响应头判定。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"exec": {
		name:           "exec",
		defaultSamples: "examples/sample_exec_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/exec/ 是命令注入（OWASP CWE-77/78）：
		// 用户输入未经 escape 拼到 shell 命令。hunter agent 通过 ;ls / `id` / |whoami
		// 等 payload + 看 stdout 回显判定。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
}

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
				return nil, nil, fmt.Errorf("未知 active profile %q（可选: active:%s）", a, strings.Join(known, " | active:"))
			}
			if _, dup := seenActive[key]; dup {
				continue
			}
			seenActive[key] = struct{}{}
			active = append(active, ap)
		case strings.HasPrefix(raw, "passive:"):
			// passive:<name>[,<name>...] 显式前缀，逗号分隔多选（如 passive:upload,lfi）。
			for _, name := range strings.Split(strings.TrimPrefix(raw, "passive:"), ",") {
				if err := addPassive(name); err != nil {
					return nil, nil, err
				}
			}
		default:
			// 裸名 = passive（向后兼容，空格分隔多选）。
			if err := addPassive(raw); err != nil {
				return nil, nil, err
			}
		}
	}
	return passive, active, nil
}
