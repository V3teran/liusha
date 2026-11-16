import type { ReactNode } from 'react'
import { Inbox, MousePointerClick, SearchX, type LucideIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

/**
 * 空态的三种成因，标题句式由成因决定、全站同一口径：
 *   - empty：还没有数据（「暂无X」），说明数据从哪来、怎么产生
 *   - no-match：有数据但当前搜索 / 筛选下为空（「没有匹配的X」），提示怎么放宽条件
 *   - unselected：主从视图里还没选中条目（「未选择X」），说明选中后能看到什么
 */
export type EmptyKind = 'empty' | 'no-match' | 'unselected'

/** no-match 的放宽方式：带筛选栏的页面可调筛选条件，只有搜索框的只能换关键词。 */
export type MatchScope = 'filter' | 'search'

const ICONS: Record<EmptyKind, LucideIcon> = {
  empty: Inbox,
  'no-match': SearchX,
  unselected: MousePointerClick,
}

const TITLE_PREFIX: Record<EmptyKind, string> = {
  empty: '暂无',
  'no-match': '没有匹配的',
  unselected: '未选择',
}

const MATCH_HINT: Record<MatchScope, string> = {
  filter: '换个关键词或调整筛选条件。',
  search: '换个关键词试试。',
}

/** 中文与西文之间留一个空格（「暂无 LLM 调用记录」「未选择 provider」）。 */
export function emptyTitle(kind: EmptyKind, noun: string): string {
  const prefix = TITLE_PREFIX[kind]
  return /^[A-Za-z0-9]/.test(noun) ? `${prefix} ${noun}` : `${prefix}${noun}`
}

type EmptyStateProps = {
  /** 名词（任务、对话、provider……），标题按成因拼出 */
  noun: string
  className?: string
} & (
  | { kind: 'empty' | 'unselected'; hint: ReactNode }
  | { kind: 'no-match'; scope?: MatchScope }
)

/** 全站统一空态：图标 + 标题 + 一句说明，居中占据所在区域。 */
export function EmptyState(props: EmptyStateProps) {
  const { kind, noun, className } = props
  const Icon = ICONS[kind]
  const hint = props.kind === 'no-match' ? MATCH_HINT[props.scope ?? 'filter'] : props.hint
  return (
    <div className={cn('flex flex-col items-center justify-center px-4 py-12 text-center', className)}>
      <span className="flex h-11 w-11 items-center justify-center rounded-full bg-surface-2 text-text-tertiary">
        <Icon aria-hidden className="h-5 w-5" />
      </span>
      <p className="mt-3 text-sm font-medium text-text">{emptyTitle(kind, noun)}</p>
      <p className="mt-1 max-w-sm text-xs leading-5 text-text-secondary">{hint}</p>
    </div>
  )
}
