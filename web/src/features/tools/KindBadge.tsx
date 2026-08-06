import type { ToolKind } from '@/api/types'
import { KIND_LABEL } from './toolMeta'

// 种类徽章：内部函数 = accent 绿；外部 CLI = amber。列表与详情面板共用（DRY）。
export function KindBadge({ kind }: { kind: ToolKind }) {
  const isFn = kind === 'function'
  return (
    <span
      className={
        'inline-flex w-fit flex-shrink-0 whitespace-nowrap rounded-full px-2 py-0.5 text-[10.5px] font-semibold leading-none ' +
        (isFn ? 'bg-accent/15 text-accent' : 'bg-amber-500/15 text-amber-500 dark:text-amber-400')
      }
    >
      {KIND_LABEL[kind]}
    </span>
  )
}
