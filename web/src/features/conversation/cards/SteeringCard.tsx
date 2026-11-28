import { Ban, ListPlus, MessageSquarePlus, PencilLine, type LucideIcon } from 'lucide-react'
import type { Message } from '@/api/types'

interface SteeringCardProps {
  msg: Message
}

interface SteeringShape {
  icon: LucideIcon
  label: string
  /** 正文前的引导词（否决的正文是原因）。 */
  lead?: string
}

function steeringShape(op: string): SteeringShape {
  switch (op) {
    case 'goal_adjusted':
      return { icon: PencilLine, label: '目标已调整' }
    case 'goal_appended':
      return { icon: MessageSquarePlus, label: '已追加到任务目标' }
    case 'action_assigned':
      return { icon: ListPlus, label: '已指派动作' }
    case 'action_rejected':
      return { icon: Ban, label: '已否决动作', lead: '原因：' }
    default:
      return { icon: PencilLine, label: op }
  }
}

// SteeringCard 是运行中引导的回执（kind=steering）：人修正方向的记录，正文是实际写入的
// 内容（新目标全文 / 追加的要求 / 指派的指令 / 否决原因）。与任务状态节点同为居中系统
// 窄条形态，正文保留换行——目标被覆盖改写后，时间线是变更唯一的历史。
export function SteeringCard({ msg }: SteeringCardProps) {
  const md = msg.metadata
  const shape = steeringShape(md?.op ?? '')
  const Icon = shape.icon
  const notes: string[] = []
  if (md?.withdrawn && md.withdrawn > 0) notes.push(`撤回 ${md.withdrawn} 个未开始的规划动作`)
  return (
    <div className="flex justify-center py-1.5" data-card="steering">
      <div className="flex max-w-[85%] flex-col items-center gap-1 rounded-xl border border-border bg-surface-2/60 px-4 py-2 text-xs" role="status">
        <span className="inline-flex flex-wrap items-center justify-center gap-x-2 gap-y-0.5">
          <span className="inline-flex items-center gap-1.5 font-medium text-accent">
            <Icon className="h-3.5 w-3.5" aria-hidden />
            {shape.label}
          </span>
          {notes.length > 0 && <span className="text-text-secondary">{notes.join(' · ')}</span>}
        </span>
        {msg.content && (
          <p className="whitespace-pre-wrap break-words text-left leading-relaxed text-text-secondary">
            {shape.lead}
            {msg.content}
          </p>
        )}
      </div>
    </div>
  )
}
