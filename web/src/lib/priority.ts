// 节点优先级的展示（与后端 explorationgraph.Priority 一一对应）：代号 + 中文名的唯一定义处。
// 探索图节点徽章与「指派动作」的优先级选择共用。

export type Priority = 'critical' | 'high' | 'medium' | 'low'

export interface PriorityMeta {
  /** 优先级代号（P0 最高） */
  code: string
  label: string
}

export const PRIORITY_META: Record<Priority, PriorityMeta> = {
  critical: { code: 'P0', label: '关键' },
  high: { code: 'P1', label: '高' },
  medium: { code: 'P2', label: '中' },
  low: { code: 'P3', label: '低' },
}

/** 从高到低（选择器的展示顺序）。 */
export const PRIORITY_ORDER: Priority[] = ['critical', 'high', 'medium', 'low']

/** 已知优先级的展示信息（未知值返回 undefined，调用方决定是否展示）。 */
export function priorityMeta(priority: string | undefined): PriorityMeta | undefined {
  return priority && priority in PRIORITY_META ? PRIORITY_META[priority as Priority] : undefined
}
