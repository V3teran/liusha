#!/bin/bash
# 临时脚本（用后即删）：审计修复波（后端）+ 工具卡合并（前端）+ push
set -e
cd /Users/Xlbula/workspace/programs/go/liusha

git add internal/planner/intelligence.go \
        internal/planner/agent_core.go \
        internal/planner/agent_planning.go \
        internal/planner/agent_plan_helpers.go \
        internal/planner/prompt_builders.go \
        internal/executor/execute_helpers.go \
        internal/executor/engine_observation_test.go \
        internal/monitor/agent.go \
        internal/monitor/tools.go \
        internal/cognition/loop.go \
        internal/cognition/telemetry.go \
        internal/framework/runtime/react_runtime.go \
        internal/framework/runtime/react_runtime_iteration.go \
        internal/framework/runtime/react_runtime_tool.go \
        internal/framework/runtime/react_runtime_summary.go \
        internal/framework/runtime/middleware_progress.go \
        internal/framework/runtime/middleware_goal_alignment.go \
        internal/framework/runtime/middleware_test.go \
        internal/framework/runtime/react_runtime_stoploss_test.go \
        internal/insight/store.go \
        internal/tools/insight.go \
        internal/toolinvocation/model.go \
        internal/toolinvocation/store.go \
        db/migrations/0173_tool_invocation_role.up.sql \
        db/migrations/0173_tool_invocation_role.down.sql \
        agents/monitor.md
git commit -m "fix: 四 agent/探索图/黑板/双纠偏全面审计修复（14 项断裂点）

planner 决策回路：
- 收敛去重：终态指纹（终态动作/result/裁决计数）无变化即跳过重复审议
  （实测任务尾段 5 次同结论、~18% LLM 浪费）；新终态变迁自动恢复审议
- 收敛 gate 纳入未裁决观察：unverified 积压不再随任务完成永久悬空
- 黑板注入：gatherContext 携带 assignment 级 insight 摘要——黑板从
  executor 自说自话变为规划者可消费的跨 agent 经验层
- aborted 动作进规划视野（含 blocked_reason）：kill 反馈回路闭合，
  禁止等效重提；观察单查合一；动作指令字面去重
- 纠偏事件 at-least-once：monitor_request 持久标记由 planner 消费
  （清标记+强制重规划），事件丢失下一轮询周期补上

图完整性：
- 动作完结必留观察（零观察时以执行总结落 state 观察）——82% 断链修复，
  图恢复共享状态面承诺
- 根目标终态（done/aborted）：图生命周期有始有终

runtime/中间件：
- OutputCount 真实填充（累计观察数）——无新输出检测从隐形硬顶恢复为
  真检测；连续失败止损单机制化（中间件恒死分支删除，阈值随装配
  下发 8/10/12）；executeTool 返回 failed 位（ErrorMask 语义下
  结果性失败此前永不计数，止损恒死——测试实证修复）
- AbortCheck：外部 kill 在 callLLM 前与每个工具执行前生效（止血
  粒度从迭代边界压到当前步骤）
- 强制总结 prompt 诚实化（删「已达最大迭代」谎报——曾污染 monitor 推理）
- 目标对齐分词支持 CJK 二元组

monitor/insight：
- kill 状态校验（仅 open/running 可 kill，终态拒改）；monitor 工具入
  共享审计链（遥测/时间线/role 归因）
- tool_invocation 增 role 列（跨 agent 行为审计，ctx 身份注入）
- read_insights priority 阈值语义（high 含 critical）；非 issue 带
  severity 显式拒回（不再静默丢弃）；死代码删除；章程节奏对齐 6 分钟"

git add web/src/lib/threadRows.ts \
        web/src/lib/threadRows.test.ts \
        web/src/features/conversation/StepTools.tsx \
        web/src/features/conversation/TimelineThread.tsx \
        web/src/features/conversation/cards/ToolCallCard.tsx \
        web/src/features/conversation/cards/ToolCallCard.test.tsx
git commit -m "feat(web): 工具动作一卡制——调用与观测同卡呈现

pairToolResults（FIFO 同配对口径）把 tool_call/tool_result 合并成
「一动作一卡」：卡内输入/输出/耗时/错误同现（业界动作卡范式），
执行中 spinner、失败红边；孤儿结果由结果卡兜底。组头预览与计数
不变。StepTools 仅渲染 call 卡，消息流中 result 并入配对。"

git push origin main
git log --oneline -3
