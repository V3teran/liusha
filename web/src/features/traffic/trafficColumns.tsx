import { createColumnHelper } from '@tanstack/react-table'
import type { TrafficSummary } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { CopyButton } from '@/components/CopyButton'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { clockTime, dateOnly, fullTime, humanBytes } from '@/lib/format'
import { methodColor, statusColor } from '@/lib/httpStatus'

const columnHelper = createColumnHelper<TrafficSummary>()

// # 抓包序号列：直显 bigserial id（单调、跨分页稳定、可引用，等价 Burp 的 #）。
// 紧凑右对齐、等宽数字，不喧宾夺主。
const seqCol = columnHelper.accessor('id', {
  header: '#',
  size: 52,
  cell: (ctx) => (
    <span className="block text-right font-mono text-[12px] tabular-nums text-muted opacity-70">{ctx.getValue()}</span>
  ),
})

// CopyableCell：长文本单元格通用形态——不换行、溢出省略号、hover title 展示全文，
// 尾随一个紧凑复制按钮（悬停行时才显形，避免每行常驻按钮的视觉噪声）。host/url 两列共用。
function CopyableCell({ value }: { value: string }) {
  const { copiedKey, copy } = useCopyToClipboard()
  const text = value || '—'
  return (
    <span className="group/cell flex items-center gap-1">
      <span className="min-w-0 flex-1 truncate font-mono text-[12.5px] text-text" title={value || undefined}>
        {text}
      </span>
      {value && (
        <CopyButton
          compact
          copied={copiedKey === 'cell'}
          onClick={() => copy('cell', value)}
          label="复制"
          className="flex-shrink-0 opacity-0 transition-opacity group-hover/cell:opacity-100"
        />
      )}
    </span>
  )
}

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
  cell: (ctx) => <CopyableCell value={ctx.getValue()} />,
})

const urlCol = columnHelper.accessor('url', {
  header: 'URL',
  size: 300, // 权重最大——占比例布局里的剩余大头
  // url 缺省时退化显示 path（旧数据/未抽取到完整 URL 的兜底），复制取实际展示值。
  cell: (ctx) => <CopyableCell value={ctx.getValue() || ctx.row.original.path} />,
})

const contentTypeCol = columnHelper.accessor('content_type', {
  header: '类型',
  size: 132,
  cell: (ctx) => {
    const ct = ctx.getValue()
    return (
      <span className="block truncate font-mono text-[12px] text-muted" title={ct || undefined}>
        {ct || '—'}
      </span>
    )
  },
})

const lengthCol = columnHelper.accessor('resp_len', {
  header: '长度',
  size: 80,
  cell: (ctx) => (
    <span className="block text-right font-mono text-[12.5px] tabular-nums text-muted">{humanBytes(ctx.getValue())}</span>
  ),
})

// 列宽为相对权重（经 columnWidthPercents 换算成百分比随容器缩放）——对齐 llmAuditColumns 模式，
// 列头/骨架/数据行不再各自维护一份 grid 字符串。
//
// showHost：仅当列表出现多个 host 时才显示 host 列（单 host 时每行重复同值是噪声，
// 与 LlmAuditPage 的 showProvider 同理）；host 排在 URL 前，符合「主机→URL」阅读顺序。
export function buildTrafficColumns(showHost: boolean) {
  return showHost
    ? [seqCol, timeCol, methodCol, statusCol, hostCol, urlCol, contentTypeCol, lengthCol]
    : [seqCol, timeCol, methodCol, statusCol, urlCol, contentTypeCol, lengthCol]
}
