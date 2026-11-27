#!/usr/bin/env bash
# lint-terminology.sh：术语契约门禁。
#
# 用法：
#   scripts/lint-terminology.sh            # 检查模式：发现残留退出 1（CI / make lint 用）
#   scripts/lint-terminology.sh count      # 报数模式：仅输出各术语残留计数（循环自检用）
#
# 规则与豁免清单即下方 FORBIDDEN 列表（含语义备忘）。
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# 扫描范围：代码 + 文档 + 配置；排除历史迁移（不可编辑）、契约保留区、第三方与本脚本自身。
# 含未跟踪文件（--untracked，仍遵守 .gitignore）：提交前本地跑与 CI 同口径，新文件不漏扫。
SCAN_ARGS=(
  --include='*.go' --include='*.ts' --include='*.tsx' --include='*.md'
  --include='*.yaml' --include='*.yml' --include='*.json'
  internal/ cmd/ web/src db/ deployments/ Makefile
)

# 排除路径（契约保留区 / 工具自身 / 第三方产物）
EXCLUDES=(
  ':!db/migrations'         # 历史迁移不可编辑（目录形式，** 通配在 git pathspec 排除里不生效）
  ':!internal/tools/manifest/tools.yaml'  # 工具清单转述第三方工具的业界用语（如 passive DNS），不是本项目概念
  ':!scripts/lint-terminology.sh'
  ':!internal/dbtest/migration_*_integration_test.go'  # 迁移测试须按旧结构写入老数据
  ':!internal/dbtest/testdata'  # 迁移测试的历史出厂内容快照
  ':!web/dist' ':!**/node_modules'
)

# 禁用术语（旧架构残留，全称匹配大小写不敏感）。
# engagement：0001 顶层会话概念，0041/0153 已删表、代码零引用——assignment 取代，防回流。
# passive / activescan / passivesession / SourceManual / SourceAuto / SourceSelf / source-self /
# manual|auto 组合（更早一代来源取值的注释写法）/ 直连下发、自主下发（已弃用的中文来源名）：
#   下发来源已收口为 direct（人工下发）/ proxy（代理捕获），见 assignment.Source（迁移 0198）。
# traffic-analysis：已退役的单 agent 角色名，现行角色见 agent_run_role_check。
# llmcall / einotools：已并入 llminvocation / registry 的旧包名。
# flows/ingest：sandbox 抓流 ingest 路由已更名为 /internal/v1/traffic/ingest。
# body_absent：断言键已统一为 body_not_contains（迁移 0200 改写存量）。
# LIUSHA_AGENT_ID：命令环境里的 run 标识已更名 LIUSHA_AGENT_RUN_ID（它是 run 的 ID，不是 agent 的）。
# 多镜像（Docker Compose）/ 多镜像部署（Docker Compose）：部署方式统一称「多镜像」，与二进制、单镜像对称。
# reseed / system_prompt / system_setting：出厂内容编译进程序、数据库只存用户自定义后退役
#   （迁移 0201）——提示词改称章程（charter），系统设置用户层为 setting_override。
# unverified / factual / verification_outcome / executor_confidence / test_plan：观察的图级
#   「置信度」改为只有假设才有的验证状态 verification_status（pending/confirmed/refuted），
#   验证结论不再双写，执行器自评置信度与 test_plan 字段删除（迁移 0208）。
# kill_action：监察决策已更名 reject_action，动作状态为 rejected（迁移 0223）。
# controlplane / 控制平面 / task_control_event / adjust_goal / inject_action：没有生产者的控制
#   事件队列已删除（迁移 0224）；生命周期只写任务行，运行中的引导走直接的 API。
# Primitives / primitives 列 / Attempt：复现配方全系统叫 repro，待验证的主张叫 Claim（迁移 0222）。
# blocked_reason：节点状态原因已统一为 state_reason（系统里没有「阻塞」状态，迁移 0221）。
# NewFromEnv / LoadWithoutLLM / APIKeyEnv / ClearProviderKey / restoreProviderEnvKey / HealthzAddr /
#   AsynqConcurrency / AsynqShutdownTimeout / QueueAgentWeight / RedisKeyPrefix / InternalAddr /
#   MaxRequestBodySize / MaxResponseBodySize / StreamName / NewStoreWithConfig：配置与 .env 统一后
#   删除或改名的代码标识符（LLM 种子与环境变量密钥路径删除；runner.listen_addr / concurrency /
#   drain_timeout_seconds；内部约定改为常量；body 上限改 _bytes）。配置键与环境变量的旧名留在升级
#   文档里，不进本清单。
# LIUSHA_API_BASE / LIUSHA_E2E_PG_DSN / LIUSHA_E2E_ADMIN / LIUSHA_PG_CONTAINER / LIUSHA_REDIS_CONTAINER /
#   newPgxPoolForE2E / ExplorationGraphClient / containerSecretEnv：e2e 改为只经公开 API 观察任务（不再
#   直连数据库，参数统一 LIUSHA_E2E_*，管理员密码共用 LIUSHA_ADMIN_INITIAL_PASSWORD）；.env 变量统一带
#   LIUSHA_ 前缀后，沙盒清洗只按前缀，不再有单独的容器密码名单。
# 裁决 / adjudicat*：评估者（evaluator）的别名。角色统一叫评估者，过程叫验证，结果叫
#   结论 / 判定；出圈原因 adjudication_failed 改名 judge_unavailable（迁移 0208）。
# webrepro / reproOrGeneric / exploit_status_code / exploit_duration_ms / FingerprintNormalizer /
#   WithFingerprintCodec："领域"同时指业务领域与配方形状，造成混淆（迁移 0234）。配方信封字段改为
#   kind（web → http、generic → steps），包改名 httprepro；证据字段与 baseline_* 对称为
#   status_code / duration_ms；主张指纹改为核心自有的中立形状 evaluator.ClaimFingerprint。
# ACTION_TYPE_META / foldTerminalActions 按类型折叠：动作不再携带类型（迁移 0233）。
# web 复现 / web 配方 / web 形状 / web.Replayer / 域信封 / 配方域：同一次改名的中文与错误文本写法
#   （形状叫 kind，取值 http / steps；信封叫配方信封）。
# 信息域 / factDomainHeadings：观察类别（ObservationCategory）曾被称为「信息域」，与业务领域撞名，
#   统一称类别。
# 夺旗：出厂场景 ctf 的名称统一为「CTF」（迁移 0236 改写存量快照）。
# LLM 配置：模型接入页是侧边栏「配置」组里的「模型」（对话模型、能力路由、检索模型——向量与重排
#   模型不是 LLM，以「模型」统称），不叫 LLM 配置。
# LLM 审计：LLM 调用记录页叫「模型调用」，与「服务器」「操作」同列「观测」组。
# registry.Allows / override.Wildcard：名单判定下沉为独立的 allowlist 包。名单的使用方（技能
#   视图、工具清单、HTTP 接口……）不再为此引用工具注册表，再由它带上模型框架与出厂自定义
#   引擎——沙盒服务端的依赖面曾因此背上这两样。
FORBIDDEN=(
  'LLM 配置'
  'LLM 审计'
  '夺旗'
  'webrepro'
  'web ?域?(复现|配方|形状)'
  'web\.Replayer'
  '域信封'
  '配方域'
  '信息域'
  'factDomainHeadings'
  'reproOrGeneric'
  'exploit_status_code'
  'exploit_duration_ms'
  'FingerprintNormalizer'
  'WithFingerprintCodec'
  'ACTION_TYPE_META'
  'Primitives'
  'parseObservationToAttempt'
  'blocked_reason'
  'BlockedReason'
  '裁决'
  'adjudicat'
  '知识图谱'
  '认知图'
  '世界模型'
  '攻击图'
  'knowledge[-_ ]\?graph'
  'KnowledgeGraph'
  'knowledgegraph'
  'KnowledgeGraphPage'
  'worldmodel'
  'WorldModel'
  'replayLegacyWeb'
  'observation_v[0-9]'
  'KeyRequestMarkers'
  'P0修复'
  'not_a_vuln'
  'vulnerability_scan'
  'indicative'
  'domain\.Profile'
  'engagement'
  'passive'
  'activescan'
  'passivesession'
  'SourceManual'
  'SourceAuto'
  'SourceSelf'
  'source-self'
  'manual ?[|/] ?auto'
  'auto ?[|/] ?manual'
  '直连下发'
  '自主下发'
  'traffic-analysis'
  'llmcall'
  'einotools'
  'flows/ingest'
  'body_absent'
  'reseed'
  'system_prompt'
  'system_setting'
  'LIUSHA_AGENT_ID'
  'unverified'
  'factual'
  'verification_outcome'
  'executor_confidence'
  'test_plan'
  'kill_action'
  'NewFromEnv'
  'LoadWithoutLLM'
  'APIKeyEnv'
  'ClearProviderKey'
  'restoreProviderEnvKey'
  'HealthzAddr'
  'AsynqConcurrency'
  'AsynqShutdownTimeout'
  'QueueAgentWeight'
  'RedisKeyPrefix'
  'InternalAddr'
  'MaxRequestBodySize'
  'MaxResponseBodySize'
  'StreamName'
  'NewStoreWithConfig'
  'LIUSHA_API_BASE'
  'LIUSHA_E2E_PG_DSN'
  'LIUSHA_E2E_ADMIN'
  'LIUSHA_PG_CONTAINER'
  'LIUSHA_REDIS_CONTAINER'
  'newPgxPoolForE2E'
  'ExplorationGraphClient'
  'containerSecretEnv'
  'controlplane'
  '控制平面'
  'task_control_event'
  'adjust_goal'
  'inject_action'
  '多镜像(部署)?（Docker Compose）'
  'registry\.Allows'
  'override\.Wildcard'
  # 内置函数工具名统一成「对象_动词」（web_search / corpus_search / command_run ……）：旧名是
  #   「动词_宾语」或别的语序。drive_browser 后面紧跟 .browser 的除外：那是沙盒里 browser-use
  #   命令报错原文里的事件名（Event handler drive_browser.browser…），Go 代码靠匹配它识别浏览器
  #   退化，恰好与旧工具名同形，不是本项目的概念。
  'search_corpus'
  'list_traffic'
  'view_traffic'
  'read_credentials'
  'write_credential'
  'read_skill'
  'run_command'
  'drive_browser'
  'observe_state'
  'evaluate_progress'
  'get_global_state'
  'publish_decision'
  'replay_for_verification'
  # 接口路径与资源命名对齐（迁移见升级说明）：模型调用记录的列表、单条不再带 search / detail；
  #   档位路由的列表与单行是同一个资源 routes；发起对话挂在 /conversations 下；列表接口的文本筛选
  #   参数统一叫 search。旧路径与旧参数名不得回流。
  'llm/invocations/search'
  'llm/invocations/detail'
  'models/routing'
  '"/chat"'
  'api/chat'
  '(agents|scenarios|skills|tools|llm/invocations)\?[a-z_=&]*q='
  # 页面地址是与后端资源同名的一级路径；模型调用页的地址与代码标识符随页面名走。
  '/config/(agents|scenarios|tools|skills|models|system)([^A-Za-z]|$)'
  'llm-audit'
  'LlmAudit'
  'llmaudit'
  # 线索机制定名前的名字不得回流：认领线索经图上的 pursues 边，规划与监察视图统一叫
  # OpenLead；「排过就从清单消失」的一次性水位（deeper_planned）已被 pursues 边加图结构推导取代。
  'advances_lead'
  'AdvancesLead'
  'HotLead'
  'deeper_planned'
  'IsOpenLead'
  'noisyFactRenderLimit'
  # 线索改为逐条交代后的名字不得回流：认领线索的契约字段 pursues_lead 并进了动作的 pursues
  #   （短号，不再是 UUID）；判定不追的 declined_leads 并进了按类别关闭的 leads——不追只是
  #   关闭的一种，另有受阻；线索上的批注 lead_declined 随之改称 lead_closed。
  'pursues_lead'
  'PursuesLead'
  'declined_lead'
  'DeclinedLead'
  'lead_declined'
  'LeadDecline'
  # 探索图的关系只剩三种（推导 generates / 滋生 triggers / 跟进 pursues）后的名字不得回流：
  #   serves（动作或线索「推进」某个派生目标，没有任何判断依赖它）、attaches 与它的改名
  #   covers（在办的动作「覆盖」某条线索的记账边；实测从未产生过，线索的处置只有认领与关闭，
  #   迁移 0262）、supersedes / superseded / ObjectiveCompletion（方向的「吸收」与「达成」：方向
  #   只作分组、没有生命周期，实测从未发生过，迁移 0262）。
  "RelServes"
  "'serves'"
  "'attaches'"
  "RelAttaches"
  '挂接'
  "RelCovers"
  "['\"]covers['\"]"
  "RelSupersedes"
  "['\"]supersedes['\"]"
  'Superseded'
  'ObjectiveCompletion'
  '被吸收'
  # provider 实测拆成「测试连接」（对话实测、回填画像）与「拉取模型列表」两个操作后，界面不再有
  #   「运行诊断 / 连接诊断」：诊断不再顺带拉列表、未填模型时也不再自动挑模型（auto_model 删除）。
  #   后端接口仍叫 diagnose（逐项诊断），不在此列。
  '运行诊断'
  '连接诊断'
  'auto_model'
  'AutoModel'
  'models_supported'
  'ModelsSupported'
  'ProviderDiagnoser([^A-Za-z]|$)'
)

# 术语豁免：某条术语在含「豁免串」的行里不算残留。写法是「术语||豁免串」——分隔符用两个竖线，
# 因为术语是正则，多选一的括号里自带竖线。
#   drive_browser：沙盒里 browser-use 命令的报错原文里有一个事件名 drive_browser.browser…
#   （Event handler drive_browser.browser…），Go 代码靠匹配它识别浏览器退化。它与旧工具名
#   恰好同形，不是本项目的概念。git grep -E 是 POSIX 正则，表达不了「后面不跟点」，所以在这里豁免。
FORBIDDEN_EXEMPT=(
  "drive_browser||Event handler drive_browser.browser"
  # 升级文档的改名对照表（deployments/README.md）：旧名 → 新名各占一行，表里的这些行不算残留。
  # 只豁免对照表的行本身；README 的其余部分照常扫，正文里用旧名仍然会被拦下。
  'search_corpus|| `search_corpus` | `corpus_search` |'
  'run_command|| `run_command` | `command_run` |'
  'drive_browser|| `drive_browser` | `browser_drive` |'
  'list_traffic|| `list_traffic` | `traffic_list` |'
  'view_traffic|| `view_traffic` | `traffic_view` |'
  'read_credentials|| `read_credentials` | `credential_read` |'
  'write_credential|| `write_credential` | `credential_write` |'
  'read_skill|| `read_skill` | `skill_read` |'
  'observe_state|| `observe_state` | `graph_read` |'
  'evaluate_progress|| `evaluate_progress` | `progress_evaluate` |'
  'get_global_state|| `get_global_state` | `task_survey` |'
  'publish_decision|| `publish_decision` | `decision_publish` |'
  'replay_for_verification|| `replay_for_verification` | `repro_replay` |'
  # 升级文档里的接口与页面地址对照表（旧地址带反引号写在表里）：只豁免表里的行本身。
  #   每条写「术语||豁免串」。
  'llm/invocations/search|| `GET /llm/invocations/search'
  'llm/invocations/detail||`/llm/invocations/detail'
  'models/routing|| `GET /models/routing'
  '"/chat"||`POST /chat`'
  '(agents|scenarios|skills|tools|llm/invocations)\?[a-z_=&]*q=||`q`'
  '/config/(agents|scenarios|tools|skills|models|system)([^A-Za-z]|$)||`/config/'
  'llm-audit||`/llm-audit`'
)

MODE="${1:-check}"

# 某条术语的全部豁免串（-F 按字面、不当作正则），一条术语可以有多条。
exempt_fragments() {
  local term="$1" pair
  for pair in "${FORBIDDEN_EXEMPT[@]}"; do
    if [ "${pair%%||*}" = "$term" ]; then
      printf '%s\n' "${pair#*||}"
    fi
  done
}

# 某条术语的残留行（已滤掉含任一豁免串的行）。
residual_lines() {
  local term="$1" frag lines
  lines="$(git grep --untracked -i -n -E "$term" -- "${SCAN_ARGS[@]}" "${EXCLUDES[@]}" 2>/dev/null || true)"
  while IFS= read -r frag; do
    [ -n "$lines" ] && lines="$(printf '%s\n' "$lines" | grep -v -F -- "$frag" || true)"
  done < <(exempt_fragments "$term")
  printf '%s' "$lines"
}

total=0
for term in "${FORBIDDEN[@]}"; do
  lines="$(residual_lines "$term")"
  hits="$(printf '%s' "$lines" | grep -c . || true)"
  if [ "$hits" != "0" ]; then
    echo "残留 [$term]: $hits"
    if [ "$MODE" != "count" ]; then
      printf '%s\n' "$lines" | head -10
    fi
    total=$((total + hits))
  fi
done

echo "TOTAL_RESIDUAL=$total"
if [ "$MODE" != "count" ] && [ "$total" != "0" ]; then
  echo "术语门禁未通过：存在废弃术语残留，见上方清单。"
  exit 1
fi
exit 0
