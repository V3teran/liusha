// LLM 审计页翻页合并——纯函数（不可变）：把下一页并入当前已渲染的响应，返回新对象。
// 同 hunter_id 追加进已有分组（保持顺序），新 hunter_id 单独成组。
import type { LLMInvocationsResponse, LLMInvocationGroup } from '../api/types'

export function mergeInvocationPage(
  current: LLMInvocationsResponse,
  next: LLMInvocationsResponse,
): LLMInvocationsResponse {
  const groups: LLMInvocationGroup[] = current.groups.map((g) => ({ ...g, invocations: [...g.invocations] }))
  const byHunter = new Map(groups.map((g) => [g.hunter_id, g]))

  for (const g of next.groups) {
    const existing = byHunter.get(g.hunter_id)
    if (existing) {
      existing.invocations = [...existing.invocations, ...g.invocations]
      existing.count += g.count
    } else {
      const copy = { ...g, invocations: [...g.invocations] }
      groups.push(copy)
      byHunter.set(g.hunter_id, copy)
    }
  }

  return {
    ...current,
    groups,
    total: current.total + next.total,
    next_after: next.next_after,
    has_more: next.has_more,
  }
}
