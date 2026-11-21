import type { TrafficConsumer, TrafficDetail } from '@/api/types'
import { Badge } from '@/components/ui/Badge'
import { fullTime } from '@/lib/format'
import { taskStatusMeta } from '@/lib/taskStatus'
import { JumpLinks } from '@/features/relations/TaskJumpLinks'
import { taskJumps } from '@/features/relations/taskJumps'
import { methodColor, statusColor } from './httpStatus'
import { RawMessageBlock } from './RawMessageBlock'
import { ErrorState } from '@/components/ui/ErrorState'
import { LoadingState } from '@/components/ui/LoadingState'

interface TrafficDetailPaneProps {
  detail: TrafficDetail | null
  loading: boolean
  error: string
  onRetry: () => void
  onClose: () => void // 关闭详情，列表恢复占满全高
}

// 消费本条流量的一个代理捕获任务：主机 + 状态，下面是这个任务的跳转（任务、对话、发现、
// 探索图）。发现属于任务——这里的「发现」是这个任务的成果，库里不记录发现来自哪条流量。
function ConsumerRow({ c }: { c: TrafficConsumer }) {
  const jumps = taskJumps(
    { taskId: c.task_id, conversationId: c.conversation_id, source: c.source, findingCount: c.finding_count, from: 'task' },
    ['task', 'conversation', 'findings', 'exploration'],
  )
  return (
    <li className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface-2/50 px-3 py-2.5">
      <span className="inline-flex min-w-0 items-center gap-1.5 text-xs">
        <span className="h-1.5 w-1.5 flex-shrink-0 rounded-full" style={{ background: taskStatusMeta(c.status).color }} aria-hidden="true" />
        <span className="truncate text-text">{c.host}</span>
        {c.status && <span className="text-text-tertiary">（{taskStatusMeta(c.status).label}）</span>}
      </span>
      <JumpLinks jumps={jumps} />
    </li>
  )
}

// 流量详情面板（Burp 式）：按需出现在列表下方，展示单条完整流量。仅在选中某行后由页面挂载。
// 顶部元信息行（#序号 · 方法 · 状态 · URL · 时间 + 关闭按钮）让详情区自明「我在看哪条」——
// 翻页/换筛选后选中行不在本页时（孤儿报文场景）仍可辨认，列表滚动后选中行滚出视口同理。
// 其下为消费任务列表（M:N，空则隐藏；每个任务带它的跳转）；再下为请求/响应两块整条报文文本（各自带 原文/美化 切换）。
export function TrafficDetailPane({ detail, loading, error, onRetry, onClose }: TrafficDetailPaneProps) {
  if (loading) {
    return <LoadingState className="h-full" />
  }
  if (error) {
    return <ErrorState message={error} onRetry={onRetry} className="h-full" />
  }
  if (!detail) return null

  // 边界防御：consumed_by 来自外部 API，形状不可信（旧后端/异常响应可能缺字段）。
  const consumers = detail.consumed_by ?? []

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* 元信息行：详情区自明条目身份（孤儿报文场景下仍可辨认）；关闭按钮同行右对齐，Esc 亦可关闭。 */}
      <div className="flex flex-shrink-0 items-center gap-2.5 border-b border-border px-4 py-2">
        <span className="flex-shrink-0 text-xs tabular-nums text-text-tertiary">#{detail.id}</span>
        <Badge dot={false} color={methodColor(detail.method)}>
          {detail.method}
        </Badge>
        <span
          className="flex-shrink-0 text-sm font-semibold tabular-nums"
          style={{ color: statusColor(detail.status_code) }}
        >
          {detail.status_code || '—'}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-text" title={detail.url || detail.path}>
          {detail.url || detail.path || '—'}
        </span>
        <span className="flex-shrink-0 font-mono text-xs text-text-secondary" title={fullTime(detail.captured_at)}>
          {fullTime(detail.captured_at)}
        </span>
        <button
          type="button"
          onClick={onClose}
          aria-label="关闭详情"
          className="inline-flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md text-text-secondary transition-colors hover:bg-surface-2 hover:text-text"
        >
          <span aria-hidden>✕</span>
        </button>
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-5 pb-4 pt-1">
        {consumers.length > 0 && (
          <section>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-text-tertiary">消费任务</h3>
            <ul className="flex flex-col gap-2">
              {consumers.map((c) => (
                <ConsumerRow key={c.task_id} c={c} />
              ))}
            </ul>
          </section>
        )}

        {/* 请求/响应：默认左右并排便于对照（Burp 做法）；仅极窄屏（<md）纵向堆叠避免字段截断。 */}
        <div className="grid min-w-0 gap-4 md:grid-cols-2">
          <RawMessageBlock title="请求" raw={detail.request_raw} emptyHint="无请求报文" />
          <RawMessageBlock title="响应" raw={detail.response_raw} emptyHint="无响应报文" />
        </div>
      </div>
    </div>
  )
}
