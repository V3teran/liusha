import { createColumnHelper } from '@tanstack/react-table'
import { Link } from 'react-router-dom'
import { Clock } from 'lucide-react'
import { Pause, Play, Square } from 'lucide-react'
import { controlTask } from '@/api/tasks'
import type { TaskSummary } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { clockTime, dateOnly, fullTime } from '@/lib/format'
import { taskStatusMeta } from '@/lib/taskStatus'

const columnHelper = createColumnHelper<TaskSummary>()

// 任务表列定义（对齐 trafficColumns 的对齐体系：数值/时间列表头与值同靠右，文本/徽章列靠左）。
// # 是页内序号（offset+index+1）——task id 是 uuid 不可直显，序号仅定位用。
// 目标列（brief）是弹性主列：weight 远大于其它列，收窄时由它承担压缩。

function buildTaskColumns(offset: number, onOperated?: () => void) {
  return [
    columnHelper.display({
      id: 'seq',
      header: '#',
      size: 44,
      meta: { align: 'right' },
      cell: (ctx) => (
        <span className="block text-right font-mono text-xs tabular-nums text-text-secondary opacity-70">
          {offset + ctx.row.index + 1}
        </span>
      ),
    }),
    columnHelper.accessor('status', {
      header: '状态',
      size: 92,
      cell: (ctx) => {
        const s = ctx.getValue()
        const paused = ctx.row.original.paused
        const meta = taskStatusMeta(s)
        // 运行中整枚徽章呼吸（点+文字）——与对话列表的状态胶囊同一视觉语法；
        // 暂停中是 active 的子状态：静态徽章，不呼吸。
        return (
          <Badge dot color={meta.color} className={s === 'active' && !paused ? 'animate-pulse' : undefined}>
            {s === 'active' ? (paused ? '已暂停' : '运行中') : meta.label}
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
          <Badge dot={false} color={ctx.getValue() === 'self' ? 'var(--source-self)' : 'var(--source-proxy)'}>
            {ctx.getValue() === 'self' ? '自主' : '代理'}
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
    columnHelper.accessor('assignment_title', {
      header: '批次',
      size: 150,
      cell: (ctx) => {
        const t = ctx.row.original
        const title = t.assignment_title || t.assignment_id.slice(0, 8)
        return (
          <span
            className="block truncate text-xs text-text-secondary"
            title={`${title} · 第 ${t.assignment_index}/${t.assignment_total} 个（${t.assignment_id}）`}
          >
            {title}
            <span className="ml-1 text-text-tertiary">
              [{t.assignment_index}/{t.assignment_total}]
            </span>
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
        if (!t.conversation_id) return <span className="text-xs text-text-secondary">—</span>
        const tab = t.source === 'self' ? 'direct' : 'proxy'
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
          <span className={`block text-right font-mono text-sm tabular-nums ${n > 0 ? 'font-semibold text-sev-high' : 'text-text-secondary'}`}>
            {n > 0 ? n : '—'}
          </span>
        )
      },
    }),
    columnHelper.accessor('step_count', {
      header: '进度',
      // 88：两位数「12 步」在窄窗下 64px 容不下（12px 字宽×4 字符 + px-4 内边距 32px）会被挤换行
      size: 88,
      meta: { align: 'right' },
      cell: (ctx) => {
        const n = ctx.getValue()
        return (
          <span
            className="block whitespace-nowrap text-right font-mono text-xs tabular-nums text-text-secondary"
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
          <em className="block text-xs font-normal not-italic text-text-secondary opacity-75">{clockTime(ctx.getValue())}</em>
        </span>
      ),
    }),
    columnHelper.accessor('ended_at', {
      header: '结束时间',
      size: 96,
      meta: { align: 'right' },
      cell: (ctx) => {
        const iso = ctx.getValue()
        if (!iso) return <span className="block text-right font-mono text-xs text-text-secondary opacity-60">—</span>
        return (
          <span className="flex flex-col gap-px whitespace-nowrap text-right font-mono text-xs text-text" title={fullTime(iso)}>
            <span>{dateOnly(iso) || '—'}</span>
            <em className="block text-xs font-normal not-italic text-text-secondary opacity-75">{clockTime(iso)}</em>
          </span>
        )
      },
    }),
    // 操作列：active 行提供 暂停/继续（按 paused 单键切换）+ 停止；终态行空占位。
    // 暂停在服务端是 task.paused 的同步 CAS——响应返回即已生效，onOperated 刷新
    // 后按钮随之翻转，无需考虑事件延迟。
    columnHelper.display({
      id: 'actions',
      header: '\u64cd\u4f5c',
      size: 84,
      cell: (ctx) => {
        const t = ctx.row.original
        if (t.status !== 'active') return <span />
        const run = (action: 'pause' | 'resume' | 'terminate') => {
          if (action === 'terminate' && !window.confirm(`\u505c\u6b62\u4efb\u52a1\u300c${t.brief.slice(0, 24)}\u2026\u300d\uff1f`)) return
          controlTask(t.id, action)
            .then(() => onOperated?.())
            .catch((e: Error) => window.alert(`\u64cd\u4f5c\u5931\u8d25\uff1a${e.message}`))
        }
        return (
          <span className="flex items-center justify-end gap-0.5" onClick={(e) => e.stopPropagation()}>
            {t.paused ? (
              <button type="button" aria-label="\u7ee7\u7eed\u4efb\u52a1" title="\u7ee7\u7eed\uff08\u4ece\u63a2\u7d22\u56fe\u8fdb\u5ea6\u6062\u590d\u6267\u884c\uff09" onClick={() => run('resume')}
                className="rounded-md p-1.5 text-text-secondary transition-colors hover:bg-surface-2 hover:text-text">
                <Play className="h-3.5 w-3.5" />
              </button>
            ) : (
              <button type="button" aria-label="\u6682\u505c\u4efb\u52a1" title="\u6682\u505c\uff08\u4fdd\u7559\u8fdb\u5ea6\uff0c\u53ef\u7ee7\u7eed\uff09" onClick={() => run('pause')}
                className="rounded-md p-1.5 text-text-secondary transition-colors hover:bg-surface-2 hover:text-text">
                <Pause className="h-3.5 w-3.5" />
              </button>
            )}
            <button type="button" aria-label="\u505c\u6b62\u4efb\u52a1" title="\u505c\u6b62\uff08\u7ec8\u6001\uff0c\u53ef\u91cd\u65b0\u4e0b\u53d1\uff09" onClick={() => run('terminate')}
              className="rounded-md p-1.5 text-text-secondary transition-colors hover:bg-sev-critical/10 hover:text-sev-critical">
              <Square className="h-3.5 w-3.5" />
            </button>
          </span>
        )
      },
    }),
  ]
}

export { buildTaskColumns }
