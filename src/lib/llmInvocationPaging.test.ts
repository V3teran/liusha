import { describe, it, expect } from 'vitest'
import { mergeInvocationPage } from './llmInvocationPaging'
import type { LLMInvocationsResponse, LLMInvocationSummary } from '../api/types'

function mkInv(id: number, hunterID: string, role: string): LLMInvocationSummary {
  return {
    id,
    request_id: `r${id}`,
    hunter_id: hunterID,
    task_id: 't1',
    provider: 'deepseek',
    model: 'deepseek-chat',
    in_tokens: 1,
    out_tokens: 1,
    cached_tokens: 0,
    latency_ms: 10,
    finish_reason: 'stop',
    error_message: '',
    role,
    created_at: '',
  }
}

describe('mergeInvocationPage', () => {
  it('同 hunter_id 追加进已有分组', () => {
    const current: LLMInvocationsResponse = {
      task_id: 't1',
      total: 1,
      next_after: 1,
      has_more: true,
      groups: [{ hunter_id: 'h1', count: 1, invocations: [mkInv(1, 'h1', 'orchestrator')] }],
    }
    const next: LLMInvocationsResponse = {
      task_id: 't1',
      total: 1,
      next_after: 2,
      has_more: false,
      groups: [{ hunter_id: 'h1', count: 1, invocations: [mkInv(2, 'h1', 'reconnaissance')] }],
    }

    const merged = mergeInvocationPage(current, next)

    expect(merged.groups).toHaveLength(1)
    expect(merged.groups[0].invocations.map((v) => v.id)).toEqual([1, 2])
    expect(merged.groups[0].count).toBe(2)
    expect(merged.total).toBe(2)
    expect(merged.next_after).toBe(2)
    expect(merged.has_more).toBe(false)
  })

  it('新 hunter_id 单独成组，不覆盖已有组', () => {
    const current: LLMInvocationsResponse = {
      task_id: 't1',
      total: 1,
      next_after: 1,
      has_more: true,
      groups: [{ hunter_id: 'h1', count: 1, invocations: [mkInv(1, 'h1', 'orchestrator')] }],
    }
    const next: LLMInvocationsResponse = {
      task_id: 't1',
      total: 1,
      next_after: 2,
      has_more: false,
      groups: [{ hunter_id: 'h2', count: 1, invocations: [mkInv(2, 'h2', 'traffic-analysis')] }],
    }

    const merged = mergeInvocationPage(current, next)

    expect(merged.groups).toHaveLength(2)
    expect(merged.groups.map((g) => g.hunter_id)).toEqual(['h1', 'h2'])
  })

  it('不 mutate 传入的 current（纯函数）', () => {
    const current: LLMInvocationsResponse = {
      task_id: 't1',
      total: 1,
      next_after: 1,
      has_more: true,
      groups: [{ hunter_id: 'h1', count: 1, invocations: [mkInv(1, 'h1', 'orchestrator')] }],
    }
    const snapshot = JSON.parse(JSON.stringify(current))
    const next: LLMInvocationsResponse = {
      task_id: 't1',
      total: 1,
      next_after: 2,
      has_more: false,
      groups: [{ hunter_id: 'h1', count: 1, invocations: [mkInv(2, 'h1', 'reconnaissance')] }],
    }

    mergeInvocationPage(current, next)

    expect(current).toEqual(snapshot)
  })
})
