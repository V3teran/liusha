import { createColumnHelper } from '@tanstack/react-table'
import { ChevronRight } from 'lucide-react'
import type { CorpusEntry } from '@/api/corpus'
import { Badge } from '@/components/ui/Badge'
import { corpusScopeLabel, corpusSourceColor, corpusSourceLabel } from '@/lib/corpus'
import { shortDateTime } from '@/lib/format'

const columnHelper = createColumnHelper<CorpusEntry>()

// 知识库表的列定义：标题撑满，范围、来源、印证、更新时间收窄。
export const corpusColumns = [
  columnHelper.accessor('title', {
    header: '知识',
    size: 420,
    cell: (ctx) => (
      <span className="block truncate" title={ctx.getValue()}>
        {ctx.getValue()}
      </span>
    ),
  }),
  columnHelper.accessor('scenario', {
    header: '适用范围',
    size: 140,
    cell: (ctx) => {
      const sc = ctx.getValue()
      return (
        <span className={sc?.deleted ? 'text-text-tertiary' : sc ? 'text-text' : 'text-text-secondary'}>
          {corpusScopeLabel(sc)}
        </span>
      )
    },
  }),
  columnHelper.accessor('source', {
    header: '来源',
    size: 96,
    cell: (ctx) => (
      <Badge dot={false} size="sm" color={corpusSourceColor(ctx.getValue())}>
        {corpusSourceLabel(ctx.getValue())}
      </Badge>
    ),
  }),
  columnHelper.accessor('confirmations', {
    header: '印证',
    size: 64,
    meta: { align: 'right' },
    cell: (ctx) => (
      <span className="block text-right font-mono text-xs tabular-nums text-text-secondary">
        {ctx.getValue() || '—'}
      </span>
    ),
  }),
  columnHelper.accessor('updated_at', {
    header: '更新',
    size: 120,
    cell: (ctx) => <span className="font-mono text-xs text-text-tertiary">{shortDateTime(ctx.getValue())}</span>,
  }),
  columnHelper.display({
    id: 'expand',
    header: '',
    size: 40,
    cell: () => <ChevronRight className="h-4 w-4 text-text-tertiary opacity-50" />,
  }),
]
