import { Link } from 'react-router-dom'
import type { TrafficConsumer, TrafficDetail } from '@/api/types'
import { RawMessageBlock } from './RawMessageBlock'

interface TrafficDetailPaneProps {
  detail: TrafficDetail | null
  loading: boolean
  error: string
  onClose: () => void // 关闭详情，列表恢复占满全高
}

// 消费本条流量的 passive task chip：场景 + host + 状态色点；有 conv_id 则整块可点跳会话。
function ConsumerChip({ c }: { c: TrafficConsumer }) {
  const inner = (
    <>
      <span className="h-1.5 w-1.5 flex-shrink-0 rounded-full" style={{ background: 'var(--accent)' }} aria-hidden="true" />
      <span className="font-medium text-text">{c.scenario_id || '—'}</span>
      <span className="text-muted">·</span>
      <span className="truncate text-muted">{c.host}</span>
      {c.status && <span className="text-muted opacity-70">({c.status})</span>}
    </>
  )
  const cls =
    'inline-flex max-w-full items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2 py-1 text-[11.5px] transition-colors'
  if (c.conv_id) {
    return (
      <Link to={`/conversations/auto?conv=${encodeURIComponent(c.conv_id)}`} className={`${cls} hover:border-accent hover:bg-accent/10`}>
        {inner}
      </Link>
    )
  }
  return (
    <span className={cls} title="该任务暂无绑定会话">
      {inner}
    </span>
  )
}

// 流量详情面板（Burp 式）：按需出现在列表下方，展示单条完整流量。仅在选中某行后由页面挂载。
// 顶部一次性呈现 method/status/url/耗时/时间 + 关闭按钮；其下为消费任务 chip 列表（M:N，空则隐藏）；
// 再下为请求/响应两块整条报文文本（各自带 原文/美化 切换）。无重复元素。
export function TrafficDetailPane({ detail, loading, error, onClose }: TrafficDetailPaneProps) {
  if (loading) {
    return <div className="flex h-full items-center justify-center text-[13px] text-muted">加载中…</div>
  }
  if (error) {
    return <div className="flex h-full items-center justify-center px-6 text-center text-[13px] text-sev-critical">⚠ {error}</div>
  }
  if (!detail) return null

  // 边界防御：consumed_by 来自外部 API，形状不可信（旧后端/异常响应可能缺字段）。
  const consumers = detail.consumed_by ?? []

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* URL 那一行与顶部状态色带均已删除。关闭按钮独占一条右对齐细行（不再绝对定位悬浮，
          避免与下方响应块表头的复制按钮重叠）；Esc 亦可关闭。 */}
      <div className="flex flex-shrink-0 justify-end px-3 pt-2">
        <button
          type="button"
          onClick={onClose}
          aria-label="关闭详情"
          className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted transition-colors hover:bg-surface-2 hover:text-text"
        >
          <span aria-hidden>✕</span>
        </button>
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-5 pb-4 pt-1">
        {consumers.length > 0 && (
          <section>
            <h3 className="mb-2 text-[12px] font-semibold uppercase tracking-wide text-muted opacity-70">消费任务</h3>
            <div className="flex flex-wrap gap-1.5">
              {consumers.map((c) => (
                <ConsumerChip key={c.task_id} c={c} />
              ))}
            </div>
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
