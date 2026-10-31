// Package runner 实现 runner 进程的执行内核：asynq agent 任务处理（幂等防重、
// 终态守卫、onboard 记账、认知循环驱动、终态回写与时间线广播）、沙箱抓流量的
// ingest HTTP 端点、热池冒烟 handler 与 LLM 用量审计桥。cmd/runner 只做进程装配。
package runner
