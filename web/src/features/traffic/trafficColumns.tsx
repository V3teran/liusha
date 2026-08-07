import { createColumnHelper } from '@tanstack/react-table'
import type { TrafficSummary } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { clockTime, dateOnly, fullTime, humanDuration } from '@/lib/format'
import { methodColor, statusColor } from '@/lib/httpStatus'

const columnHelper = createColumnHelper<TrafficSummary>()

const timeCol = columnHelper.accessor('captured_at', {
  header: '时间',
  size: 96,
  cell: (ctx) => {
    const iso = ctx.getValue()
    return (
      <span className="flex flex-col gap-px whitespace-nowrap font-mono text-xs text-text" title={fullTime(iso)}>
        <span>{dateOnly(iso) || '—'}</span>
        <em className="block text-[10.5px] font-normal not-italic text-muted opacity-75">{clockTime(iso)}</em>
      </span>
    )
  },
})

const methodCol = columnHelper.accessor('method', {
  header: '方法',
  size: 72,
  cell: (ctx) => {
    const m = ctx.getValue()
    return (
      <Badge dot={false} color={methodColor(m)}>
        {m || '—'}
      </Badge>
    )
  },
})

const statusCol = columnHelper.accessor('status_code', {
  header: '状态',
  size: 68,
  cell: (ctx) => {
    const code = ctx.getValue()
    return (
      <span className="font-mono text-[12.5px] font-semibold tabular-nums" style={{ color: statusColor(code) }}>
        {code || '—'}
      </span>
    )
  },
})

const hostCol = columnHelper.accessor('host', {
  header: '主机',
  size: 148,
  cell: (ctx) => (
    <span className="block overflow-hidden truncate font-mono text-[12.5px] text-text" title={ctx.getValue()}>
      {ctx.getValue() || '—'}
    </span>
  ),
})

const pathCol = columnHelper.accessor('path', {
  header: '路径',
  size: 240, // 权重最大——占比例布局里的剩余大头
  cell: (ctx) => (
    <span className="block overflow-hidden truncate font-mono text-[12.5px] text-text" title={ctx.getValue()}>
      {ctx.getValue() || '—'}
    </span>
  ),
})

const durationCol = columnHelper.accessor('duration_ms', {
  header: '耗时',
  size: 80,
  cell: (ctx) => (
    <span className="font-mono text-[12.5px] tabular-nums text-muted">{humanDuration(ctx.getValue())}</span>
  ),
})

// 列宽为相对权重（经 columnWidthPercents 换算成百分比随容器缩放）——对齐 llmAuditColumns 模式，
// 列头/骨架/数据行不再各自维护一份 grid 字符串。
//
// showHost：仅当列表出现多个 host 时才显示 host 列（单 host 时每行重复同值是噪声，
// 与 LlmAuditPage 的 showProvider 同理）；host 排在路径前，符合「主机→路径」阅读顺序。
export function buildTrafficColumns(showHost: boolean) {
  return showHost
    ? [timeCol, methodCol, statusCol, hostCol, pathCol, durationCol]
    : [timeCol, methodCol, statusCol, pathCol, durationCol]
}
