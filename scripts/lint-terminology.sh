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
# LLM 配置：模型接入页统一称「模型配置」——向量与重排模型不是 LLM，页面承载全部模型接入
#   （对话 provider、档位路由、检索模型密钥），以「模型」统称。
FORBIDDEN=(
  'LLM 配置'
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
)

MODE="${1:-check}"

total=0
for term in "${FORBIDDEN[@]}"; do
  hits=$(git grep --untracked -i -E "$term" -- "${SCAN_ARGS[@]}" "${EXCLUDES[@]}" 2>/dev/null | wc -l | tr -d ' ')
  if [ "$hits" != "0" ]; then
    echo "残留 [$term]: $hits"
    if [ "$MODE" != "count" ]; then
      git grep --untracked -i -n -E "$term" -- "${SCAN_ARGS[@]}" "${EXCLUDES[@]}" 2>/dev/null | head -10
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
