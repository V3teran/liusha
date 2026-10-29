import { createColumnHelper } from '@tanstack/react-table'
import { Link } from 'react-router-dom'
import { Clock } from 'lucide-react'
import type { TaskSummary } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { clockTime, dateOnly, fullTime } from '@/lib/format'
import { scanStatusMeta } from '@/lib/scanStatus'

const columnHelper = createColumnHelper<TaskSummary>()

// 任务表列定义（对齐 trafficColumns 的对齐体系：数值/时间列表头与值同靠右，文本/徽章列靠左）。
// # 是页内序号（offset+index+1）——task id 是 uuid 不可直显，序号仅定位用。
// 目标列（brief）是弹性主列：weight 远大于其它列，收窄时由它承担压缩。

function buildTaskColumns(offset: number) {
  return [
    columnHelper.display({
      id: 'seq',
      header: '#',
      size: 44,
      meta: { align: 'right' },
      cell: (ctx) => (
        <span className="block text-right font-mono text-xs tabular-nums text-muted opacity-70">
          {offset + ctx.row.index + 1}
        </span>
      ),
    }),
    columnHelper.accessor('status', {
      header: '状态',
      size: 92,
      cell: (ctx) => {
        const s = ctx.getValue()
        const meta = scanStatusMeta(s)
        return (
          <Badge dot color={meta.color} className={s === 'active' ? 'animate-pulse' : undefined}>
            {s === 'active' ? '运行中' : meta.label}
          </Badge>
        )
      },
    }),
    columnHelper.accessor('brief', {
      header: '目标',
      size: 320,
      cell: (ctx) => (
        <span className="block truncate text-sm text-text" title={ctx.getValue()}>
          {ctx.getValue()}
        </span>
      ),
    }),
    columnHelper.accessor('source', {
      header: '来源',
      size: 88,
      cell: (ctx) => (
        <span className="flex items-center gap-1">
          <Badge dot={false} color={ctx.getValue() === 'manual' ? 'var(--source-manual)' : 'var(--source-auto)'}>
            {ctx.getValue() === 'manual' ? '主动' : '被动'}
          </Badge>
          {ctx.row.original.scheduled && (
            <Clock
              className="h-3 w-3 shrink-0 text-text-tertiary"
              aria-label="定时触发"
            >
              <title>{`定时触发 · ${ctx.row.original.cron || 'cron'}`}</title>
            </Clock>
          )}
        </span>
      ),
    }),
    columnHelper.accessor('assignment_id', {
      header: '批次ID',
      size: 116,
      cell: (ctx) => {
        const t = ctx.row.original
        const short = ctx.getValue().slice(0, 8)
        return (
          <span
            className="block truncate font-mono text-xs text-muted"
            title={`批次 ${ctx.getValue()}`}
          >
            {short} · {t.assignment_ndx}/{t.assignment_total}
          </span>
        )
      },
    }),
    columnHelper.display({
      id: 'conversation',
      header: '对话',
      size: 76,
      cell: (ctx) => {
        const t = ctx.row.original
        if (!t.conversation_id) return <span className="text-xs text-muted">—</span>
        const tab = t.source === 'manual' ? 'manual' : 'auto'
        return (
          <Link
            to={`/conversations/${tab}?conv=${encodeURIComponent(t.conversation_id)}`}
            onClick={(e) => e.stopPropagation()}
            className="text-sm whitespace-nowrap text-accent hover:underline"
          >
            打开 →
          </Link>
        )
      },
    }),
    columnHelper.accessor('finding_count', {
      header: '漏洞',
      size: 48,
      meta: { align: 'right' },
      cell: (ctx) => {
        const n = ctx.getValue()
        return (
          <span className={`block text-right font-mono text-sm tabular-nums ${n > 0 ? 'font-semibold text-sev-high' : 'text-muted'}`}>
            {n > 0 ? n : '—'}
          </span>
        )
      },
    }),
    columnHelper.accessor('step_count', {
      header: '进度',
      size: 64,
      meta: { align: 'right' },
      cell: (ctx) => {
        const n = ctx.getValue()
        return (
          <span
            className="block text-right font-mono text-xs tabular-nums text-muted"
            title={ctx.row.original.status === 'active' ? `已执行 ${n} 步` : `共 ${n} 步`}
          >
            {n} 步
          </span>
        )
      },
    }),
    columnHelper.accessor('created_at', {
      header: '创建时间',
      size: 96,
      meta: { align: 'right' },
      cell: (ctx) => (
        <span className="flex flex-col gap-px whitespace-nowrap text-right font-mono text-xs text-text" title={fullTime(ctx.getValue())}>
          <span>{dateOnly(ctx.getValue()) || '—'}</span>
          <em className="block text-xs font-normal not-italic text-muted opacity-75">{clockTime(ctx.getValue())}</em>
        </span>
      ),
    }),
    columnHelper.accessor('ended_at', {
      header: '结束时间',
      size: 96,
      meta: { align: 'right' },
      cell: (ctx) => {
        const iso = ctx.getValue()
        if (!iso) return <span className="block text-right font-mono text-xs text-muted opacity-60">—</span>
        return (
          <span className="flex flex-col gap-px whitespace-nowrap text-right font-mono text-xs text-text" title={fullTime(iso)}>
            <span>{dateOnly(iso) || '—'}</span>
            <em className="block text-xs font-normal not-italic text-muted opacity-75">{clockTime(iso)}</em>
          </span>
        )
      },
    }),
  ]
}

export { buildTaskColumns }
