import { describe, expect, it } from 'vitest'
import { invocationStatus } from './llmInvocationStatus'

describe('invocationStatus', () => {
  it('error_message 存在 → critical/失败', () => {
    const s = invocationStatus({ latency_ms: 100, error_message: '连接超时', error_kind: '', finish_reason: '' })
    expect(s).toEqual({ level: 'critical', label: '失败', detail: '连接超时' })
  })

  it('latency_ms 达到看门狗上限 → critical/超时（优先于 finish_reason）', () => {
    const s = invocationStatus({ latency_ms: 300_000, error_message: '', error_kind: '', finish_reason: 'stop' }, 300_000)
    expect(s?.level).toBe('critical')
    expect(s?.label).toBe('超时')
    expect(s?.detail).toBe('达到 LLM 看门狗上限（5 分钟），未获得响应')
  })

  // 上限以系统配置为准：页面不另记一个出厂值，详情里写的是实际生效的值。
  it('上限未知时不判超时；上限不同时同一个耗时的结论与文案都跟着变', () => {
    const row = { latency_ms: 300_000, error_message: '', error_kind: '', finish_reason: 'stop' }
    expect(invocationStatus(row)).toBeNull()
    expect(invocationStatus(row, 600_000)).toBeNull()
    expect(invocationStatus({ ...row, latency_ms: 120_000 }, 120_000)?.detail).toBe(
      '达到 LLM 看门狗上限（2 分钟），未获得响应',
    )
  })

  it('finish_reason=length → warn/截断', () => {
    const s = invocationStatus({ latency_ms: 100, error_message: '', error_kind: '', finish_reason: 'length' })
    expect(s).toEqual({ level: 'warn', label: '截断', detail: '输出达到 max_tokens 上限，内容可能不完整' })
  })

  it('finish_reason=content_filter → warn/内容过滤', () => {
    const s = invocationStatus({ latency_ms: 100, error_message: '', error_kind: '', finish_reason: 'content_filter' })
    expect(s?.label).toBe('内容过滤')
  })

  it('正常完成（stop）→ null，不渲染徽章', () => {
    expect(invocationStatus({ latency_ms: 100, error_message: '', error_kind: '', finish_reason: 'stop' })).toBeNull()
  })

  it('finish_reason 为空（非流式常见）且无错误 → null', () => {
    expect(invocationStatus({ latency_ms: 100, error_message: '', error_kind: '', finish_reason: '' })).toBeNull()
  })
})
