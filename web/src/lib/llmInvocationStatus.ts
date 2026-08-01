// LLM 调用「异常状态」判定：失败 / 看门狗超时 / 输出截断 / 内容过滤。
// 单一真相源：审计表状态列 + 详情抽屉头部徽章共用，不再各自判断一遍。
//
// 设计取舍：正常完成（finish_reason=stop/tool_calls 或为空）不返回徽章——只在异常时才
// 提高视觉权重，对齐 FindingsPage 的克制风格（正常态不占视觉噪音，异常态才需要一眼看到）。
import type { LLMInvocationSummary } from '@/api/types'
import { isTimeout } from './llmTiming'

export type StatusLevel = 'critical' | 'warn'

export interface InvocationStatus {
  level: StatusLevel
  label: string
  /** 悬停详情：失败给出错误原文，截断/过滤给出简短说明。 */
  detail: string
}

type StatusInput = Pick<LLMInvocationSummary, 'latency_ms' | 'error_message' | 'finish_reason'>

/** 返回 null 表示正常完成，调用方不渲染状态徽章。 */
export function invocationStatus(v: StatusInput): InvocationStatus | null {
  if (v.error_message) {
    return { level: 'critical', label: '失败', detail: v.error_message }
  }
  if (isTimeout(v)) {
    return { level: 'critical', label: '超时', detail: '达到 LLM 看门狗上限（5 分钟），未获得响应' }
  }
  if (v.finish_reason === 'length') {
    return { level: 'warn', label: '截断', detail: '输出达到 max_tokens 上限，内容可能不完整' }
  }
  if (v.finish_reason === 'content_filter') {
    return { level: 'warn', label: '内容过滤', detail: '模型服务商的内容安全策略拦截了本次输出' }
  }
  return null
}
