import { CheckCircle2, XCircle, Rocket, Square, type LucideIcon } from 'lucide-react'
import type { TimelineEventMeta } from '@/api/types'

interface TaskStatusCardProps {
  md: TimelineEventMeta
}

// 任务状态节点的呈现形状：图标 + 主文案 + 强调色（系统事件，非 agent 产出）。
interface StatusShape {
  icon: LucideIcon
  label: string
  color: string // 文案与图标色
}

function statusShape(md: TimelineEventMeta): StatusShape {
  switch (md.status) {
    case 'dispatched':
      return { icon: Rocket, label: '已下发扫描', color: 'var(--color-accent)' }
    case 'completed':
      return { icon: CheckCircle2, label: '扫描完成', color: '#34d399' }
    case 'aborted':
      return { icon: Square, label: '扫描已停止', color: 'var(--color-muted)' }
    case 'failed':
      return { icon: XCircle, label: '扫描执行失败', color: 'var(--color-sev-critical)' }
    default:
      return { icon: Square, label: md.status, color: 'var(--color-muted)' }
  }
}

// 停止原因的人读映射：stop_reason 是终态的唯一「怎么停的」事实源——不渲染时
// 用户终止与自然收敛在时间线上逐像素相同（实测 stop_reason='control-plane:
// terminate' 的任务显示绿色「扫描完成」）。
const STOP_REASON_LABELS: Record<string, string> = {
  'control-plane: terminate': '手动终止',
  context_cancelled: '执行被取消',
  task_converged: '目标收敛',
  max_steps_reached: '达到步数上限',
}

// 报告字段行：completed/aborted 携 steps/findings 与停止原因，failed 携 reason。
// stop_reason 有映射才渲染（未知值原样展示，宁多一字不瞒一况）。
function reportFields(md: TimelineEventMeta): string[] {
  const parts: string[] = []
  if (md.steps && md.steps > 0) parts.push(`${md.steps} 步`)
  if (md.findings !== undefined && md.findings > 0) parts.push(`${md.findings} 个发现`)
  if (md.stop_reason) parts.push(STOP_REASON_LABELS[md.stop_reason] ?? md.stop_reason)
  if (md.reason) parts.push(md.reason)
  return parts
}

// TaskStatusCard 是任务生命周期节点（kind=task_status）：下发/完成/停止/失败的
// 时间线边界。系统事件居中窄条呈现（业界 CI 状态行范式），与 agent 卡（左侧
// 归属卡）和会话消息（左右分置）三者形态区分——一眼分辨「谁在说话」与「系统裁决」。
export function TaskStatusCard({ md }: TaskStatusCardProps) {
  const shape = statusShape(md)
  const fields = reportFields(md)
  const Icon = shape.icon
  return (
    <div className="flex justify-center py-1.5" data-card="task-status">
      <div
        className="inline-flex max-w-[85%] flex-wrap items-center justify-center gap-x-2 gap-y-0.5 rounded-full border border-border bg-surface-2/60 px-4 py-1.5 text-xs"
        role="status"
      >
        <span className="inline-flex items-center gap-1.5 font-medium" style={{ color: shape.color }}>
          <Icon className="h-3.5 w-3.5" />
          {shape.label}
        </span>
        {fields.length > 0 && (
          <span className="text-muted">
            {fields.map((p, i) => (
              <span key={i}>
                {i > 0 && ' · '}
                {p}
              </span>
            ))}
          </span>
        )}
      </div>
    </div>
  )
}
