// 任务状态 → 中文标签 + 色。单一真相源：任务列表 / 任务详情 / 会话列表 / 会话详情共用，
// 确保「进行中/已完成/已中止」全站一套词（不以布尔 running 二元判定，aborted 不会被
// 显示成"已完成"）。
//
// 状态来自任务层真实态（conversation.run_status）：
//   active=进行中 / completed=已完成 / aborted=已中止 / 空=会话（纯聊天无关联任务）。
export interface TaskStatusMeta {
  label: string
  key: string // active / completed / aborted / idle —— 驱动 css class
  color: string
}

// 色引用设计 token（随主题、已按 WCAG AA 校准），返回 CSS 值字符串。
export function taskStatusMeta(status: string | undefined): TaskStatusMeta {
  switch (status) {
    case 'active':
      return { label: '进行中', key: 'active', color: 'var(--status-success)' }
    case 'completed':
      return { label: '已完成', key: 'completed', color: 'var(--status-info)' }
    case 'aborted':
      return { label: '已中止', key: 'aborted', color: 'var(--status-neutral)' }
    default:
      return { label: '对话', key: 'idle', color: 'var(--text-tertiary)' } // 空/纯聊天
  }
}
