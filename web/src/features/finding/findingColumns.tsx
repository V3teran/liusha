import { createColumnHelper } from '@tanstack/react-table'
import { ChevronRight, StickyNote } from 'lucide-react'
import type { FindingRow } from '@/api/types'
import { Badge } from '@/components/ui/Badge'
import { severityColorOf, severityLabel } from '@/lib/severity'
import { cn } from '@/lib/utils'
import { sourceColor, sourceLabel } from '@/lib/source'
import { findingStatusMeta } from './findingStatus'

const columnHelper = createColumnHelper<FindingRow>()

// 发现台账表的列定义：单点声明，表外壳（列宽/骨架/空态/行交互）走 DataTable 通用组件。
export const findingColumns = [
  columnHelper.accessor('seq', {
    header: '#',
    size: 64,
    meta: { align: 'right' },
    cell: (ctx) => (
      <span className="block text-right text-xs tabular-nums text-text-tertiary">{ctx.getValue()}</span>
    ),
  }),
  columnHelper.accessor('severity', {
    header: '严重度',
    size: 90,
    cell: (ctx) => {
      const sev = ctx.getValue()
      return <Badge color={severityColorOf(sev)} size="sm">{severityLabel[sev.toLowerCase()] || sev}</Badge>
    },
  }),
  columnHelper.accessor('summary', {
    header: '发现',
    size: 400,
    cell: (ctx) => {
      const f = ctx.row.original
      return (
        <span className="flex items-center gap-2 overflow-hidden">
          <span className="truncate" title={f.summary}>{f.summary}</span>
          {f.triage_note && (
            <StickyNote className="h-3.5 w-3.5 flex-shrink-0 text-text-tertiary" aria-label="有处置备注" />
          )}
        </span>
      )
    },
  }),
  columnHelper.accessor('class', {
    header: '类型',
    size: 130,
    cell: (ctx) => {
      const f = ctx.row.original
      return (
        <span
          className={cn('block truncate text-xs', f.class === 'other' ? 'text-text-tertiary' : 'text-text-secondary')}
          title={f.cwe ? `${f.class_name} · ${f.cwe}` : f.class_name}
        >
          {f.class_name}
        </span>
      )
    },
  }),
  columnHelper.display({
    id: 'location',
    header: '地址',
    size: 240,
    cell: (ctx) => {
      const f = ctx.row.original
      return (
        <span
          className="block truncate font-mono text-xs text-text-secondary"
          title={`${f.target?.method || ''} ${f.host}${f.target?.path || ''}`}
        >
          {f.target?.method} {f.host}
          {f.target?.path}
        </span>
      )
    },
  }),
  columnHelper.accessor('source', {
    header: '来源',
    size: 90,
    cell: (ctx) => {
      const source = ctx.getValue()
      return (
        <Badge dot={false} size="sm" color={sourceColor(source)}>
          {sourceLabel(source)}
        </Badge>
      )
    },
  }),
  columnHelper.accessor('status', {
    header: '状态',
    size: 96,
    cell: (ctx) => {
      const meta = findingStatusMeta(ctx.getValue())
      return <Badge color={meta.color} size="sm">{meta.label}</Badge>
    },
  }),
  columnHelper.display({
    id: 'expand',
    // 箭头只示意「整行可点开」：表头给读屏一个列名，图标本身不播报
    header: () => <span className="sr-only">详情</span>,
    size: 48,
    cell: () => <ChevronRight aria-hidden className="h-4 w-4 text-text-tertiary opacity-50" />,
  }),
]
