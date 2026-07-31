// 扫描任务状态 → 中文标签 + 色。单一真相源：ConversationList（列表项）与 ChatView（顶部状态栏）
// 共用，确保「进行中/已完成/已中止」全站一套词、不再二元误判（顶部曾用布尔 running 把 aborted
// 错显示成"已完成"）。
//
// 状态来自 active_scan.status / passive_session.status（任务层真实态）：
//   active=进行中 / completed=已完成 / aborted=已中止 / 空=会话（纯聊天无关联扫描）。
export interface ScanStatusMeta {
  label: string
  key: string // active / done / aborted / idle —— 驱动 css class
  color: string
}

export function scanStatusMeta(status: string | undefined): ScanStatusMeta {
  switch (status) {
    case 'active':
      return { label: '进行中', key: 'active', color: '#22c55e' }
    case 'completed':
      return { label: '已完成', key: 'done', color: '#3b82f6' }
    case 'aborted':
      return { label: '已中止', key: 'aborted', color: '#94a3b8' }
    default:
      return { label: '对话', key: 'idle', color: '#64748b' } // 空/纯聊天
  }
}
