// LLM 调用时延分档：把延迟/TTFT 判成 good/warn/bad 三档，供审计表配色。
//
// 阈值取自业界日志页（NewAPI usage-logs）的做法：
//   - 总时长优先按**吞吐**判（输出快就不算慢，长输出本该耗时久）；输出太少时吞吐没有统计意义，
//     退化为按纯耗时判。
//   - TTFT 按纯耗时判（首 token 与输出量无关）。
import type { LLMInvocationSummary } from '../api/types'

export type TimingLevel = 'good' | 'warn' | 'bad'

/** 后端看门狗 step_llm_timeout_seconds=300 → 达到该值即超时打点，非真实耗时。 */
export const LLM_TIMEOUT_MS = 300_000

/** 吞吐分档（输出 token/秒）：>=30 好，>=15 一般，更低差。 */
export function throughputLevel(tokensPerSecond: number): TimingLevel {
  if (tokensPerSecond >= 30) return 'good'
  if (tokensPerSecond >= 15) return 'warn'
  return 'bad'
}

/** 纯耗时分档（秒）：<5s 好，<10s 一般，更久差。 */
export function durationLevel(seconds: number): TimingLevel {
  if (seconds < 5) return 'good'
  if (seconds < 10) return 'warn'
  return 'bad'
}

/** 输出 token 少于此值时吞吐无统计意义，改按纯耗时判档。 */
const MIN_TOKENS_FOR_THROUGHPUT = 100

/**
 * 总时长分档：输出足够多时按吞吐判（长输出耗时久是正常的），否则按纯耗时判。
 */
export function latencyLevel(latencyMs: number, outTokens: number): TimingLevel {
  const seconds = latencyMs / 1000
  if (outTokens < MIN_TOKENS_FOR_THROUGHPUT || seconds <= 0) return durationLevel(seconds)
  return throughputLevel(outTokens / seconds)
}

/** TTFT 分档（首 token 与输出量无关，纯看耗时）。 */
export function ttftLevel(ttftMs: number): TimingLevel {
  return durationLevel(ttftMs / 1000)
}

/** 是否为看门狗超时打点（此时延迟数字不代表真实响应耗时）。 */
export function isTimeout(v: Pick<LLMInvocationSummary, 'latency_ms'>): boolean {
  return v.latency_ms >= LLM_TIMEOUT_MS
}

/**
 * 输出速率文案（t/s）；无法计算时返回空串。
 * 超时打点不给速率——分母是被截断的看门狗时长，算出来没有意义。
 */
export function throughputLabel(v: Pick<LLMInvocationSummary, 'latency_ms' | 'out_tokens'>): string {
  if (v.latency_ms <= 0 || v.out_tokens <= 0 || isTimeout(v)) return ''
  return `${(v.out_tokens / (v.latency_ms / 1000)).toFixed(1)} t/s`
}
