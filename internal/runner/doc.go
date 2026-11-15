// Package runner 实现 runner 进程的执行内核：asynq agent 任务处理（幂等防重、
// 终态守卫、onboard 记账、认知循环驱动、沙盒资源释放、终态回写与时间线广播）、
// 沙盒浏览器流量回灌的 ingest HTTP 端点与 LLM 用量审计桥。internal/app/runner 只做组件装配。
package runner
