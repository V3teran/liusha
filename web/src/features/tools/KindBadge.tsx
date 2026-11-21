import type { ToolKind } from '@/api/types'
import { Badge } from '@/components/ui/Badge'
import { KIND_LABEL } from './toolMeta'

// 种类徽章：内部函数用强调色，外部 CLI 用分类色橙——两者只区分「是哪一类」，不表达好坏。
// 走全站 Badge 的软底配方（色字对自身软底按 AA 校准），不另写一套配色。
export function KindBadge({ kind }: { kind: ToolKind }) {
  return (
    <Badge dot={false} size="sm" color={kind === 'function' ? 'var(--accent)' : 'var(--cat-orange)'}>
      {KIND_LABEL[kind]}
    </Badge>
  )
}
