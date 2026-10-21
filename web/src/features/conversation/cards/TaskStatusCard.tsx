import { CheckCircle2, XCircle, Rocket, Square, type LucideIcon } from 'lucide-react'
import type { ScanEvent } from '@/api/types'

interface TaskStatusCardProps {
  md: ScanEvent
}

// 任务状态节点的呈现形状：图标 + 主文案 + 强调色（系统事件，非 agent 产出）。
interface StatusShape {
  icon: LucideIcon
  label: string
  color: string // 文案与图标色
}

function statusShape(md: ScanEvent): StatusShape {
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

// 报告字段行：completed 携 steps/findings/stop_why，aborted/failed 携 reason。
// 字段缺失（旧消息/降级路径）整体隐藏该行。
function reportFields(md: ScanEvent): string[] {
  const parts: string[] = []
  if (md.steps && md.steps > 0) parts.push(`${md.steps} 步`)
  if (md.findings !== undefined && md.findings > 0) parts.push(`${md.findings} 个漏洞`)
  if (md.stop_why) parts.push(`收敛于 ${md.stop_why}`)
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
        className="inline-flex max-w-[85%] flex-wrap items-center justify-center gap-x-2 gap-y-0.5 rounded-full border border-border bg-surface-2/60 px-3.5 py-1.5 text-[12px]"
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
