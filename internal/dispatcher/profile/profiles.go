// Package profile 为每个 MoveKind 提供预置的 Dispatcher Profile。
//
// 调用方（buildDispatcher）用 Profiles() 批量注册全部5个 Profile，
// 也可按需调用单个工厂函数以覆盖 systemPrompt 或 Budget。
package profile

import (
	"github.com/V3teran/liusha/internal/actor"
	"github.com/V3teran/liusha/internal/dispatcher"
)

// Profiles 返回全部5个 MoveKind 的默认 Profile 列表，传入的 systemPrompt
// 作为各 Profile 系统提示的前缀（外部注入场景人设/角色定位）。
func Profiles(systemPrompt string) []dispatcher.Profile {
	return []dispatcher.Profile{
		Enumerate(systemPrompt),
		Probe(systemPrompt),
		Exploit(systemPrompt),
		Escalate(systemPrompt),
		Persist(systemPrompt),
	}
}

// Enumerate 返回信息收集/枚举 Move 的 Profile。
// 目标：枚举攻击面（web目录/端口/云资产/域对象/CTF初探），产出资产清单，不打洞不写 finding。
func Enumerate(systemPrompt string) dispatcher.Profile {
	const body = `
## 当前任务：信息收集/枚举（enumerate）

你的唯一产出是**资产清单**——web目录/端点/参数/技术栈/云资源/域对象/已有流量。

### 工作流
1. 先 list_traffic 看已有流量，避免重复枚举。
2. 用 run_command 执行枚举工具：
   - Web：ffuf/katana/gospider/httpx 枚举目录端点
   - 网络：nmap/masscan 扫端口/服务
   - 云：AWS CLI/az/gcloud 枚举资源/IAM策略
   - 域：BloodHound/ldapdomaindump 枚举域对象/信任关系
   - CTF：端口扫描/源码分析/题目说明解读
3. 每发现一个资产，write_lead(clue/observation) 写入黑板供后续阶段读取。
4. 读 search_corpus 查历史同类目标的已知枚举打法，缩短路径。
5. 枚举完成后调 done，附上资产清单摘要。

### 约束
- 不写 finding（那是 probe/exploit 的职责）。
- 不做破坏性操作，只读/枚举。
- 枚举到新资产及时 write_lead(clue)，避免信息丢失。`

	return dispatcher.Profile{
		MoveKind:     actor.MoveKindEnumerate,
		SystemPrompt: systemPrompt + body,
		Tools: []string{
			"list_traffic", "view_traffic",
			"search_corpus", "write_corpus",
			"write_lead", "mark_insight",
			"read_tooling_skill", "read_vuln_skill",
			"read_credentials",
			"run_command",
			"done",
		},
		Budget: actor.Budget{
			MaxSteps:          40,
			MaxTokens:         80000,
			WatchdogSecs:      300,
			CompactionTrigger: 0.70,
			CriticInterval:    5,
		},
		Settle: actor.SettleConfig{
			Threshold:    0.85,
			Directive:    "枚举预算接近上限，立即输出当前已发现的资产清单，然后调用 done 结束。",
			AllowedTools: []string{"write_lead", "done"},
		},
		MaxExecutions: 1,
	}
}

// Probe 返回漏洞探测 Move 的 Profile。
// 目标：对已知资产做系统性弱点探测（扫描/fuzz/初步PoC），产出疑似漏洞 lead，不坐实。
func Probe(systemPrompt string) dispatcher.Profile {
	const body = `
## 当前任务：漏洞探测/弱点发现（probe）

你接到的是一个**已知资产**（端点/服务/功能点），系统性地探测弱点，
输出疑似漏洞线索（write_lead 或 write_finding），不做深度利用确认。

### 工作流
1. list_traffic 找到目标资产的已有流量。
2. read_vuln_skill 拉取对应漏洞类型探测指南（sqli/xss/ssrf/idor/deserialization 等）。
3. 用 replay_traffic + 改写参数做基础 fuzz，或 run_command 调专项工具：
   - Web：sqlmap/dalfox/nuclei/wapiti
   - 二进制：fuzz工具/静态分析
   - 云：权限边界探测/S3公开检测/策略误配扫描
   - 域：Kerberoasting/AS-REP Roast/ACL检测
4. 发现可疑信号立即 write_lead(clue)；对高置信度信号初步写 write_finding（标注 confidence=assumed）。
5. 探测完成调 done，汇总发现的线索。

### 约束
- 探测粒度：单资产，不做横向枚举。
- 产出以 lead(clue) 为主，PoC 确认前 finding 标 assumed（防 false positive 污染报告）。
- 不做破坏性利用，只探测不坐实。`

	return dispatcher.Profile{
		MoveKind:     actor.MoveKindProbe,
		SystemPrompt: systemPrompt + body,
		Tools: []string{
			"list_traffic", "view_traffic", "replay_traffic",
			"read_credentials",
			"read_findings", "write_finding",
			"search_corpus", "write_corpus",
			"write_lead", "mark_insight",
			"read_tooling_skill", "read_vuln_skill",
			"run_command",
			"done",
		},
		Budget: actor.Budget{
			MaxSteps:          50,
			MaxTokens:         100000,
			WatchdogSecs:      360,
			CompactionTrigger: 0.70,
			CriticInterval:    5,
		},
		Settle: actor.SettleConfig{
			Threshold:    0.85,
			Directive:    "探测预算接近上限，把已发现的疑似弱点写入 lead 或 finding（标 assumed），然后调用 done 结束。",
			AllowedTools: []string{"write_lead", "write_finding", "done"},
		},
		MaxExecutions: 2,
	}
}

// Exploit 返回漏洞利用 Move 的 Profile。
// 目标：打穿单个攻击面，证明可利用，写 finding 含完整 PoC。
func Exploit(systemPrompt string) dispatcher.Profile {
	const body = `
## 当前任务：漏洞利用（exploit）

你接到的是一个**具体攻击面 + 已有线索**，深挖并证明可利用，写 finding。

### 工作流
0. brief 含「已观察到 [现象]，证据在此」→ 直接复现确认 → write_finding → done。
1. baseline：目标可达 + 凭据有效（list_traffic 看 tool=browser 流量首选）。
2. read_credentials 拿凭据；浏览器链路直接 run_command browser-use open <url>。
3. 优先 replay_traffic(id, mods) 改字段重发（越权/IDOR/fuzz）；无现成流量才 run_command curl。
4. 打穿后 write_finding（含 evidence/repro，severity 保守评）。
5. read_findings 自查防 dedup → done。

### 纪律
- 只打本攻击面，不扩散侦察。
- write_corpus 沉淀可复用打法（有值才写，防噪音）。
- browser_use 用于带登录态的页面交互，不用于枚举。`

	return dispatcher.Profile{
		MoveKind:     actor.MoveKindExploit,
		SystemPrompt: systemPrompt + body,
		Tools: []string{
			"list_traffic", "view_traffic", "replay_traffic",
			"read_credentials", "write_credential",
			"read_findings", "write_finding", "update_finding",
			"search_corpus", "write_corpus",
			"write_lead", "mark_insight",
			"read_tooling_skill", "read_vuln_skill",
			"run_command", "browser_use",
			"done",
		},
		Budget: actor.Budget{
			MaxSteps:          60,
			MaxTokens:         120000,
			WatchdogSecs:      600,
			CompactionTrigger: 0.70,
			CriticInterval:    5,
		},
		Settle: actor.SettleConfig{
			Threshold:    0.85,
			Directive:    "利用预算接近上限，把已有最强证据写入 finding，然后调用 done 结束。",
			AllowedTools: []string{"write_finding", "update_finding", "done"},
		},
		MaxExecutions: 3,
	}
}

// Escalate 返回权限提升/横向移动 Move 的 Profile。
// 目标：从立足点扩展攻击面（本机提权/域提权/云IAM提权/跨主机横移）。
func Escalate(systemPrompt string) dispatcher.Profile {
	const body = `
## 当前任务：权限提升/横向移动（escalate）

你持有一个立足点（shell/凭据/会话），现在扩展攻击面：
- **提权**：低权限→高权限（SUID/sudo/云IAM权限提升/域提权）
- **横移**：跨主机/跨服务/跨账户扩展

### 工作流
1. read_credentials 拿当前可用凭据。
2. 提权探测：
   - Web/Linux：SUID/sudo/内核漏洞/服务配置错误
   - 云：IAM权限边界探测/跨账户AssumeRole/服务角色滥用
   - 域：Kerberoasting/AS-REP Roast/GPO滥用/委派攻击
3. 横移探测：
   - 内网扫描（nmap/netexec）
   - SSH跳板/RDP横移
   - 共享存储/配置文件泄露的凭据
   - 云服务间权限链（Lambda→S3→RDS）
4. 每个成功的提权/横移 write_finding（含证据和复现步骤）。
5. write_credential 记录新获得的凭据。
6. write_lead 记录内网拓扑和访问路径。
7. done 总结扩展战果，标明下一步建议。

### 纪律
- 用已有凭据测试，不暴力破解。
- 横移聚焦高价值目标（域控/堡垒机/云管理后台）。
- write_corpus 沉淀环境特定的提权/横移技巧。`

	return dispatcher.Profile{
		MoveKind:     actor.MoveKindEscalate,
		SystemPrompt: systemPrompt + body,
		Tools: []string{
			"list_traffic", "view_traffic", "replay_traffic",
			"read_credentials", "write_credential",
			"read_findings", "write_finding", "update_finding",
			"search_corpus", "write_corpus",
			"write_lead", "mark_insight",
			"read_tooling_skill", "read_vuln_skill",
			"run_command", "browser_use",
			"done",
		},
		Budget: actor.Budget{
			MaxSteps:          60,
			MaxTokens:         120000,
			WatchdogSecs:      600,
			CompactionTrigger: 0.70,
			CriticInterval:    5,
		},
		Settle: actor.SettleConfig{
			Threshold:    0.85,
			Directive:    "提权/横移预算接近上限，把已成功的提权/横移路径写入 finding，然后调用 done 结束。",
			AllowedTools: []string{"write_finding", "write_credential", "write_lead", "done"},
		},
		MaxExecutions: 3,
	}
}

// Persist 返回后渗透/持久化 Move 的 Profile。
// 目标：数据采集/持久化/C2/影响评估，变现已获权限。
func Persist(systemPrompt string) dispatcher.Profile {
	const body = `
## 当前任务：后渗透/持久化（persist）

你已在目标上获得立足点，现在：
- **数据采集**：敏感数据外带/flag提交/数据库dump
- **持久化**：后门/定时任务/服务劫持
- **影响评估**：权限范围/数据价值/业务影响

### 工作流
1. run_command 执行信息收集（id/whoami/uname/env/ps/netstat/find -perm -4000）。
2. 搜索高价值数据：
   - Web：数据库凭据/API密钥/用户数据/备份文件
   - CTF：flag文件/隐藏路径/加密数据
   - 云：S3数据/RDS快照/Lambda环境变量
   - 域：NTDS.dit/GPO配置/敏感文档
3. 数据外带/持久化：
   - Web：SQL dump/文件下载/反弹shell/webshell
   - CTF：flag提交/答案验证
   - 云：数据导出/快照复制/权限持久化
   - 域：DCSync/Golden Ticket/SID History
4. 每个高价值发现 write_finding（含证据截图/命令输出/数据样本）。
5. write_lead(observation) 记录影响范围和后续建议。
6. write_corpus 沉淀同类环境的后渗透/持久化技巧。
7. done 总结后渗透战果，标明影响范围。

### 纪律
- 操作在已授权范围内；破坏性操作（格式化/删数据/DoS）不执行。
- 数据外带遵守 RoE（交战规则）：仅采样不全量，敏感数据脱敏。
- CTF场景：flag获取后立即提交验证，避免误判。`

	return dispatcher.Profile{
		MoveKind:     actor.MoveKindPersist,
		SystemPrompt: systemPrompt + body,
		Tools: []string{
			"list_traffic", "view_traffic",
			"read_credentials", "write_credential",
			"read_findings", "write_finding", "update_finding",
			"search_corpus", "write_corpus",
			"write_lead", "mark_insight",
			"read_tooling_skill", "read_vuln_skill",
			"run_command",
			"done",
		},
		Budget: actor.Budget{
			MaxSteps:          50,
			MaxTokens:         100000,
			WatchdogSecs:      480,
			CompactionTrigger: 0.70,
			CriticInterval:    5,
		},
		Settle: actor.SettleConfig{
			Threshold:    0.85,
			Directive:    "后渗透预算接近上限，把已获取的高价值发现写入 finding，标明影响范围，然后调用 done 结束。",
			AllowedTools: []string{"write_finding", "write_lead", "done"},
		},
		MaxExecutions: 2,
	}
}
