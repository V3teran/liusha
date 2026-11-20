/**
 * 落点范围：从其它页面跳来时只看被点击的那个元素对应的内容。
 *
 * 任务和对话是同一件工作的两个入口（一个任务至多一个对话）：从对话出发按「对话」限定、显示
 * 对话标题；从任务及其它地方（发现、流量、LLM 调用、探索图）出发按「任务」限定、显示任务目标。
 * 任务页、发现页、探索图页都接受同一对 URL 参数 task / conversation。
 */
export type ScopeKind = 'task' | 'conversation'

export interface Scope {
  kind: ScopeKind
  id: string
}

/** 从筛选字段取范围：对话优先（两者不会同时出现在跳转链接里），都没有时为 null。 */
export function scopeOf(fields: { task?: string; conversation?: string }): Scope | null {
  if (fields.conversation) return { kind: 'conversation', id: fields.conversation }
  if (fields.task) return { kind: 'task', id: fields.task }
  return null
}

/** 范围对应的 URL 查询串（task=… 或 conversation=…）。 */
export function scopeQuery(scope: Scope): string {
  return `${scope.kind}=${encodeURIComponent(scope.id)}`
}

/** 清除范围后的筛选字段（task 与 conversation 一并清空）。 */
export const NO_SCOPE = { task: '', conversation: '' } as const
