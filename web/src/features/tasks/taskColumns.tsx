import { createColumnHelper } from '@tanstack/react-table'
import { Clock } from 'lucide-react'
import { Pause, Play, Square } from 'lucide-react'
import { TokenPair } from '@/components/TokenPair'
import type { TaskSummary } from '@/api/types'
import { Badge } from '@/components/ui/Badge'
import { SeverityTally } from '@/features/finding/SeverityCounts'
import { clockTime, dateOnly, fullTime } from '@/lib/format'
import { taskStatusMeta } from '@/lib/taskStatus'
import { sourceColor, sourceLabel } from '@/lib/source'

const columnHelper = createColumnHelper<TaskSummary>()

// 任务目标的单行指称（确认弹层、下拉选项）：省略号只在真截断时出现——短目标带「…」是误导。
// 缺省 24 字适合句中指称；下拉展开时有富余宽度，可放宽以保留区分度。
export function goalHead(goal: string, max = 24): string {
  return goal.length > max ? `${goal.slice(0, max)}…` : goal
}

// 任务表列定义。对齐只有一个声明点（lib/tableLayout 的 meta.align），表头与单元格同侧：
// 数值列（序号、Tokens、进度）靠右，文本、时间、徽章列靠左，操作列跟着它的按钮靠右。
// # 是页内序号（offset+index+1）——task id 是 uuid 不可直显，序号仅定位用。
// 目标列是弹性主列：weight 远大于其它列，收窄时由它承担压缩。
//
// 全表设计宽度合计 ≈1130px：1440 视口（侧栏展开）内容区刚好完整放下全部列，不出横向滚动。
// 发现列从总数换成五个档的计数后宽了 64px，从批次、进度、操作三列各让出一点、目标列再让 18px 补回来
// （进度列两位数只占一半、操作列两个图标钮共 54px 都实测有富余；目标列截断处有悬停全文）。

function buildTaskColumns(
  offset: number,
  /** 控制动作统一上交宿主（TaskTable）：暂停/停止由宿主过确认弹层后执行。 */
  requestControl?: (task: TaskSummary, action: 'pause' | 'resume' | 'terminate') => void,
) {
  return [
    columnHelper.display({
      id: 'seq',
      header: '#',
      size: 44,
      meta: { align: 'right' },
      cell: (ctx) => (
        <span className="block text-xs tabular-nums text-text-tertiary">{offset + ctx.row.index + 1}</span>
      ),
    }),
    columnHelper.accessor('status', {
      header: '状态',
      size: 92,
      cell: (ctx) => {
        const s = ctx.getValue()
        const paused = ctx.row.original.paused
        const meta = taskStatusMeta(s)
        // 运行中色点呼吸（文字恒定，保对比度）——与对话列表的状态点同一视觉语法；
        // 暂停中是 active 的子状态：静态徽章，不呼吸。
        return (
          <Badge dot pulse={s === 'active' && !paused} color={meta.color}>
            {s === 'active' ? (paused ? '已暂停' : '运行中') : meta.label}
          </Badge>
        )
      },
    }),
    columnHelper.accessor('goal', {
      header: '目标',
      // 162：为发现列让出 18px 后的主列宽度（全表合计见上）；超长目标截断 + 悬停全文
      size: 162,
      cell: (ctx) => (
        <span className="block truncate text-sm text-text" title={ctx.getValue()}>
          {ctx.getValue()}
        </span>
      ),
    }),
    columnHelper.accessor('source', {
      header: '来源',
      // 108：4 字来源徽章 + 定时触发时钟图标
      size: 108,
      cell: (ctx) => (
        <span className="flex items-center gap-1">
          <Badge dot={false} color={sourceColor(ctx.getValue())}>
            {sourceLabel(ctx.getValue())}
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
      // 124：批内序号 + 批次名；批次名截断，悬停给全名
      size: 124,
      cell: (ctx) => {
        const t = ctx.row.original
        // 进度前置（扫到第几个一目了然），悬停给批次全名
        const title = t.assignment_title
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
    columnHelper.accessor('findings_by_severity', {
      header: '发现',
      // 128：五个数各留两位（「12/34/56/78/90」约 91px）；数的顺序见悬停提示
      size: 128,
      meta: { headerTitle: '按严重程度：严重 / 高危 / 中危 / 低危 / 信息' },
      cell: (ctx) => <SeverityTally counts={ctx.getValue()} />,
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
          return <span className="block font-mono text-xs text-text-tertiary">—</span>
        // TokenPair：与对话页/审计页同一呈现（↑入↓出，中性色——token 是
        // 度量数据不是风险信号，不借发现色板）
        return (
          <span className="block">
            <TokenPair inTokens={t.tokens_in} outTokens={t.tokens_out} />
          </span>
        )
      },
    }),
    columnHelper.accessor('step_count', {
      header: '进度',
      // 72：「999 步」约 37px + 内边距 24px；再窄会折行
      size: 72,
      meta: { align: 'right' },
      cell: (ctx) => {
        const n = ctx.getValue()
        return (
          <span
            className="block whitespace-nowrap text-xs tabular-nums text-text-secondary"
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
      cell: (ctx) => (
        <span className="flex flex-col gap-px whitespace-nowrap font-mono text-xs text-text" title={fullTime(ctx.getValue())}>
          <span>{dateOnly(ctx.getValue()) || '—'}</span>
          <em className="block text-xs font-normal not-italic text-text-tertiary">{clockTime(ctx.getValue())}</em>
        </span>
      ),
    }),
    columnHelper.accessor('ended_at', {
      header: '结束时间',
      size: 96,
      cell: (ctx) => {
        const iso = ctx.getValue()
        if (!iso) return <span className="block font-mono text-xs text-text-tertiary">—</span>
        return (
          <span className="flex flex-col gap-px whitespace-nowrap font-mono text-xs text-text" title={fullTime(iso)}>
            <span>{dateOnly(iso) || '—'}</span>
            <em className="block text-xs font-normal not-italic text-text-tertiary">{clockTime(iso)}</em>
          </span>
        )
      },
    }),
    // 操作列：进行中的行提供 暂停/继续（按 paused 单键切换）+ 停止；已结束的行留空——
    // 列宽固定，留空不会让布局跳动；不可用的控件留在每一行只会稀释可操作行的信号。
    // 为什么不能操作由详情面板（禁用态 + 说明）交代。暂停在服务端是 task.paused 的同步 CAS——
    // 响应返回即已生效，onOperated 刷新后按钮随之翻转，无需考虑事件延迟。
    columnHelper.display({
      id: 'actions',
      header: '操作',
      // 80：两个图标钮共 54px + 内边距 24px
      size: 80,
      // 表头跟着按钮靠右：靠左的「操作」两个字下面是一排贴着右缘的按钮，对不上
      meta: { align: 'right' },
      cell: (ctx) => {
        const t = ctx.row.original
        if (t.status !== 'active') return null
        const run = (action: 'pause' | 'resume' | 'terminate') => {
          requestControl?.(t, action)
        }
        return (
          <span className="flex items-center justify-end gap-0.5" onClick={(e) => e.stopPropagation()}>
            {/* 暂停/继续合并为随 paused 翻转的单切换钮；停止独立（终态，走确认） */}
            <button
              type="button"
              aria-label={t.paused ? '继续任务' : '暂停任务'}
              title={t.paused ? '继续（从探索图进度恢复执行）' : '暂停（保留进度，可继续）'}
              onClick={() => run(t.paused ? 'resume' : 'pause')}
              className="rounded-md p-1.5 text-text-secondary transition-colors hover:bg-surface-2 hover:text-text"
            >
              {t.paused ? <Play className="h-3.5 w-3.5" /> : <Pause className="h-3.5 w-3.5" />}
            </button>
            <button
              type="button"
              aria-label="停止任务"
              title="停止（终态，可重新下发）"
              onClick={() => run('terminate')}
              className="rounded-md p-1.5 text-text-secondary transition-colors hover:bg-status-error/10 hover:text-status-error"
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
