import { useRef } from 'react'
import { Link } from 'react-router-dom'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Clock3, Pencil, Trash2 } from 'lucide-react'
import type { Conversation } from '@/api/types'
import { taskJumps } from '@/features/relations/taskJumps'

const ITEM_CLASS =
  'flex w-full cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5 text-left text-xs text-text outline-none data-[highlighted]:bg-surface-2'

/**
 * 对话行的「⋯」菜单（Radix DropdownMenu，层级走 app-menu）：跳转（只看这个对话的任务、漏洞、
 * 探索图，规则见 taskJumps；纯聊天没有任务，不显示）+ 重命名 + 删除（任务进行中禁删）。
 * 由 ConversationList 持有重命名 / 删除逻辑，本组件只负责菜单本身。
 */
export function ConvMenu({
  conv,
  deleting,
  onOpenChange,
  onRename,
  onDelete,
}: {
  conv: Conversation
  deleting: string | null
  /** 菜单开合（列表据此暂停轮询，不在操作中途刷新列表） */
  onOpenChange?: (open: boolean) => void
  onRename: (c: Conversation) => void
  onDelete: (c: Conversation) => void
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
  // 选了「重命名」：菜单收起后焦点留给行内的重命名输入框，不交还给 ⋯ 按钮
  const toRenameRef = useRef(false)
  return (
    <DropdownMenu.Root modal={false} onOpenChange={onOpenChange}>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          title="更多"
          aria-label="更多"
          // 不冒泡到行：点 ⋯ 不等于点开这个对话
          onClick={(e) => e.stopPropagation()}
          className="mt-0.5 flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md text-text-secondary opacity-0 hover:bg-surface focus:opacity-100 group-hover:opacity-100 data-[state=open]:opacity-100"
        >
          <span className="text-base leading-none">⋯</span>
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="app-menu min-w-[148px] rounded-lg border border-border bg-surface p-1 shadow-lg"
          align="end"
          sideOffset={4}
          collisionPadding={8}
          onClick={(e) => e.stopPropagation()}
          onCloseAutoFocus={(e) => {
            if (toRenameRef.current) {
              e.preventDefault()
              toRenameRef.current = false
            }
          }}
        >
          {jumps.map((j) => (
            <DropdownMenu.Item key={j.target} asChild className={ITEM_CLASS}>
              <Link to={j.to}>
                <j.icon className="h-3.5 w-3.5" />
                {j.label}
              </Link>
            </DropdownMenu.Item>
          ))}
          {jumps.length > 0 && <DropdownMenu.Separator className="my-1 h-px bg-border-subtle" />}
          <DropdownMenu.Item
            onSelect={() => {
              toRenameRef.current = true
              onRename(conv)
            }}
            className={ITEM_CLASS}
          >
            <Pencil className="h-3.5 w-3.5" />
            重命名
          </DropdownMenu.Item>
          {conv.run_status === 'active' ? (
            <DropdownMenu.Item
              disabled
              title="任务进行中，先在对话页「停止任务」，停止后再删除"
              className={`${ITEM_CLASS} cursor-default text-text-secondary opacity-50`}
            >
              <Clock3 className="h-3.5 w-3.5" />
              任务进行中 · 先停止再删
            </DropdownMenu.Item>
          ) : (
            <DropdownMenu.Item
              disabled={deleting === conv.id}
              onSelect={() => onDelete(conv)}
              className={`${ITEM_CLASS} data-[highlighted]:bg-status-error/15 data-[highlighted]:text-status-error data-[disabled]:opacity-50`}
            >
              <Trash2 className="h-3.5 w-3.5" />
              删除对话
            </DropdownMenu.Item>
          )}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
