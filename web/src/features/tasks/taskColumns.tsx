import { createColumnHelper } from '@tanstack/react-table'
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
        // 进度前置（扫到第几个一目了然），悬停给批次全名——ID 对人无意义
        const title = t.assignment_title || t.assignment_id.slice(0, 8)
        return (
          <span
            className="block truncate text-xs text-text-secondary"
            title={title}
          >
            <span className="font-mono text-text-tertiary">
              [{t.assignment_index}/{t.assignment_total}]
            </span>
            <span className="ml-1">{title}</span>
          </span>
        )
      },
    }),
    columnHelper.accessor('finding_count', {
      header: '漏洞',
      size: 64,
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
    columnHelper.display({
      id: 'tokens',
      header: 'Tokens',
      // 双向满宽形态「↑1.31M ↓183.33K」实测需 ~120px + 内边距；104 会折行
      size: 128,
      meta: { align: 'right' },
      cell: (ctx) => {
        const t = ctx.row.original
        if (!t.tokens_in && !t.tokens_out)
          return <span className="block text-right font-mono text-xs text-text-secondary opacity-40">—</span>
        // 双向与对话页同形式（↑输入 ↓输出）：输入=上下文成本、输出=生成成本，
        // 合计会抹掉成本结构差异
        const compact = (n: number) =>
          n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1)}M` : n >= 1_000 ? `${(n / 1_000).toFixed(1)}k` : String(n)
        return (
          <span
            className="block whitespace-nowrap text-right font-mono text-xs tabular-nums text-text-secondary"
            title={`输入 ${t.tokens_in.toLocaleString()} · 输出 ${t.tokens_out.toLocaleString()}`}
          >
            <span className="text-sev-info">↑{compact(t.tokens_in)}</span>{' '}
            <span className="text-sev-high">↓{compact(t.tokens_out)}</span>
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
        // 按钮恒显、按状态禁用（affordance 可见：操作位不随数据闪没，位置稳定）
        const terminal = t.status !== 'active'
        const run = (action: 'pause' | 'resume' | 'terminate') => {
          if (action === 'terminate') {
            // 省略号只在真截断时出现——短 brief 带「…」是误导
            const head = t.brief.length > 24 ? `${t.brief.slice(0, 24)}…` : t.brief
            if (!window.confirm(`停止任务「${head}」？`)) return
          }
          controlTask(t.id, action)
            .then(() => onOperated?.())
            .catch((e: Error) => window.alert(`操作失败：${e.message}`))
        }
        const disabledCls = 'cursor-not-allowed opacity-30 hover:bg-transparent hover:text-text-secondary'
        const idleCls = 'transition-colors hover:bg-surface-2 hover:text-text'
        return (
          <span className="flex items-center justify-end gap-0.5" onClick={(e) => e.stopPropagation()}>
            <button
              type="button"
              aria-label="暂停任务"
              title={terminal ? '任务已结束' : t.paused ? '已暂停' : '暂停（保留进度，可继续）'}
              disabled={terminal || t.paused}
              onClick={() => run('pause')}
              className={`rounded-md p-1.5 text-text-secondary ${terminal || t.paused ? disabledCls : idleCls}`}
            >
              <Pause className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              aria-label="继续任务"
              title={terminal ? '任务已结束' : t.paused ? '继续（从探索图进度恢复执行）' : '运行中'}
              disabled={terminal || !t.paused}
              onClick={() => run('resume')}
              className={`rounded-md p-1.5 text-text-secondary ${terminal || !t.paused ? disabledCls : idleCls}`}
            >
              <Play className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              aria-label="停止任务"
              title={terminal ? '任务已结束' : '停止（终态，可重新下发）'}
              disabled={terminal}
              onClick={() => run('terminate')}
              className={`rounded-md p-1.5 text-text-secondary ${terminal ? disabledCls : 'hover:bg-status-error/10 hover:text-status-error'}`}
            >
              <Square className="h-3.5 w-3.5" />
            </button>
          </span>
        )
      },
    }),
  ]
}

export { buildTaskColumns }
