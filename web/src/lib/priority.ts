// 主张优先级的展示（与后端 explorationgraph.Priority 一一对应）：代号 + 中文名的唯一定义处。
// 探索图上假设与结果的角标、详情面板用它；渗透测试里它就是漏洞的严重度。动作与目标没有优先级。

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

/** 已知优先级的展示信息（未知值返回 undefined，调用方决定是否展示）。 */
export function priorityMeta(priority: string | undefined): PriorityMeta | undefined {
  return priority && priority in PRIORITY_META ? PRIORITY_META[priority as Priority] : undefined
}
