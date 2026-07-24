// 漏洞处置态（triage）→ 中文标签 + 色。单一真相源：漏洞管理页列表项 + 状态下拉共用。
// 对齐后端 finding.status 五态（DB CHECK 约束，见迁移 0081）与 DefectDojo/GitHub Security 通用生命周期。
//
//   open           待处理（新漏洞默认，write_finding 落库即此态）
//   confirmed      已确认（人工核实为真实漏洞，待修）
//   fixed          已修复
//   false_positive 误报（LLM 挖错 / 非真实漏洞）
//   accepted       接受风险（真实但业务决定不修）
export type FindingStatus = 'open' | 'confirmed' | 'fixed' | 'false_positive' | 'accepted'

export interface FindingStatusMeta {
  label: string
  key: string // 驱动 css class：open/confirmed/fixed/fp/accepted
  color: string
}

// 五态元信息。open 琥珀（待办感）、confirmed 红（真漏洞待修）、fixed 绿（闭环）、
// false_positive 灰（作废）、accepted 蓝（知悉不修）。
const META: Record<FindingStatus, FindingStatusMeta> = {
  open: { label: '待处理', key: 'open', color: '#f59e0b' },
  confirmed: { label: '已确认', key: 'confirmed', color: '#ef4444' },
  fixed: { label: '已修复', key: 'fixed', color: '#22c55e' },
  false_positive: { label: '误报', key: 'fp', color: '#94a3b8' },
  accepted: { label: '接受风险', key: 'accepted', color: '#3b82f6' },
}

export function findingStatusMeta(status: string | undefined): FindingStatusMeta {
  return META[(status as FindingStatus)] ?? META.open
}

// 下拉选项顺序（triage 流转的自然顺序：待处理→确认→修复，误报/接受风险为旁支）。
export const FINDING_STATUS_OPTIONS: { value: FindingStatus; label: string }[] = [
  { value: 'open', label: META.open.label },
  { value: 'confirmed', label: META.confirmed.label },
  { value: 'fixed', label: META.fixed.label },
  { value: 'false_positive', label: META.false_positive.label },
  { value: 'accepted', label: META.accepted.label },
]
