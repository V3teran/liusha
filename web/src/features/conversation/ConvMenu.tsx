import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { Bug, Clock3, ListChecks, Network, Pencil, Trash2 } from 'lucide-react'
import type { Conversation } from '@/api/types'

/** ⋯ 菜单（挂 body 的 portal）：跳转（任务/探索图/漏洞）+ 重命名 + 删除
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
  return createPortal(
    <div
      className="fixed z-[1000] min-w-[148px] rounded-lg border border-border bg-surface p-1 shadow-lg"
      style={style}
      onClick={(e) => e.stopPropagation()}
    >
      {/* 任务/探索图/漏洞都属于会话所属的任务，按任务 ID 跳；纯聊天会话没有任务，不显示 */}
      {conv.task_id && (
        <>
          <Link
            to={`/tasks?task=${encodeURIComponent(conv.task_id)}`}
            className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-text hover:bg-surface-2"
            onClick={onClose}
          >
            <ListChecks className="h-3.5 w-3.5" />
            任务
          </Link>
          <Link
            to={`/exploration-graph?task=${encodeURIComponent(conv.task_id)}`}
            className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-text hover:bg-surface-2"
            onClick={onClose}
          >
            <Network className="h-3.5 w-3.5" />
            探索图
          </Link>
        </>
      )}
      {/* 无洞不跳（与任务详情同一规则）：入口必须通向有内容的落点 */}
      {conv.task_id && (conv.finding_count ?? 0) > 0 && (
        <Link
          to={`/findings?task=${encodeURIComponent(conv.task_id)}`}
          className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-text hover:bg-surface-2"
          onClick={onClose}
        >
          <Bug className="h-3.5 w-3.5" />
          漏洞
        </Link>
      )}
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
