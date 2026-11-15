import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { Clock3, Pencil, Trash2 } from 'lucide-react'
import type { Conversation } from '@/api/types'
import { taskJumps } from '@/features/relations/taskJumps'

/** ⋯ 菜单（挂 body 的 portal）：跳转（任务/漏洞/探索图）+ 重命名 + 删除
 * （任务进行中禁删）。由 ConversationList 持有状态，本组件纯展示分发。 */
export function ConvMenu({
  conv,
  style,
  deleting,
  onClose,
  onRename,
  onDelete,
}: {
  conv: Conversation
  style: { top: string; left: string }
  deleting: string | null
  onClose: () => void
  onRename: (c: Conversation, ev: React.MouseEvent) => void
  onDelete: (c: Conversation, ev: React.MouseEvent) => void
}) {
  const jumps = conv.task_id
    ? taskJumps(
        {
          taskId: conv.task_id,
          conversationId: conv.id,
          source: conv.source ?? '',
          findingCount: conv.finding_count ?? 0,
          from: 'conversation',
        },
        ['task', 'findings', 'exploration'],
      )
    : []
  return createPortal(
    <div
      className="fixed z-[1000] min-w-[148px] rounded-lg border border-border bg-surface p-1 shadow-lg"
      style={style}
      onClick={(e) => e.stopPropagation()}
    >
      {/* 跳转：只看这个对话的任务、漏洞与探索图（规则见 taskJumps）；纯聊天对话没有任务，不显示 */}
      {jumps.map((j) => (
        <Link
          key={j.target}
          to={j.to}
          className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-text hover:bg-surface-2"
          onClick={onClose}
        >
          <j.icon className="h-3.5 w-3.5" />
          {j.label}
        </Link>
      ))}
      <button
        type="button"
        onClick={(ev) => onRename(conv, ev)}
        className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-text hover:bg-surface-2"
      >
        <Pencil className="h-3.5 w-3.5" />
        重命名
      </button>
      {conv.run_status === 'active' ? (
        <button
          type="button"
          disabled
          title="任务进行中，先在对话页「停止任务」，停止后再删除"
          className="flex w-full cursor-default items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-text-secondary opacity-50"
        >
          <Clock3 className="h-3.5 w-3.5" />
          任务进行中 · 先停止再删
        </button>
      ) : (
        <button
          type="button"
          disabled={deleting === conv.id}
          onClick={(ev) => onDelete(conv, ev)}
          className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-text hover:bg-status-error/15 hover:text-status-error disabled:opacity-50"
        >
          <Trash2 className="h-3.5 w-3.5" />
          删除对话
        </button>
      )}
    </div>,
    document.body,
  )
}
