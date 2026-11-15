import { createPortal } from 'react-dom'
import { Clock3, Pencil, Trash2 } from 'lucide-react'
import type { Conversation } from '@/api/types'

/** ⋯ 菜单（挂 body 的 portal）：对话行的操作——重命名 + 删除（跳转是对话自身的出口，在对话顶栏）
 * （任务进行中禁删）。由 ConversationList 持有状态，本组件纯展示分发。 */
export function ConvMenu({
  conv,
  style,
  deleting,
  onRename,
  onDelete,
}: {
  conv: Conversation
  style: { top: string; left: string }
  deleting: string | null
  onRename: (c: Conversation, ev: React.MouseEvent) => void
  onDelete: (c: Conversation, ev: React.MouseEvent) => void
}) {
  return createPortal(
    <div
      className="fixed z-[1000] min-w-[148px] rounded-lg border border-border bg-surface p-1 shadow-lg"
      style={style}
      onClick={(e) => e.stopPropagation()}
    >
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
