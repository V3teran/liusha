import type { OverviewSnapshot } from '@/api/overview'
import { compactNumber } from '@/lib/format'
import { SEVERITIES, severityLabel } from '@/lib/severity'
import { RangePills } from './RangePills'
import { TrendArea } from './TrendArea'
import { Panel, PanelLink } from './Panel'

/** 近 N 天每天新增的发现（总数）；各级别多少在当天的明细里。 */
export function FindingsTrend({
  daily,
  days,
  onDaysChange,
  className,
}: {
  daily: OverviewSnapshot['findings']['daily']
  days: number
  onDaysChange: (days: number) => void
  className?: string
}) {
  const data = daily.map((d) => ({ date: d.date, value: SEVERITIES.reduce((n, s) => n + (d.counts[s] ?? 0), 0) }))
  const total = data.reduce((n, d) => n + d.value, 0)
  return (
    <Panel
      title={`近 ${days} 天新增发现`}
      meta={`${total} 条`}
      className={className}
      action={
        <div className="flex items-center gap-3">
          <RangePills value={days} onChange={onDaysChange} />
          <PanelLink to="/findings">发现台账</PanelLink>
        </div>
      }
    >
      <TrendArea
        label="每天新增的发现"
        data={data}
        color="var(--cat-orange)"
        format={(v) => `${v} 条`}
        axis={String}
        detail={(i) => {
          const c = daily[i].counts
          const parts = SEVERITIES.filter((s) => (c[s] ?? 0) > 0).map((s) => `${severityLabel[s]} ${c[s]}`)
          return [`${data[i].value} 条`, ...(parts.length ? [parts.join('、')] : [])]
        }}
      />
    </Panel>
  )
}

/**
 * 近 N 天每天的 LLM token 消耗（输入 + 输出），调用次数与错误在当天的明细里。
 */
export function UsageTrend({
  usage,
  days,
  onDaysChange,
  className,
}: {
  usage: OverviewSnapshot['usage']
  days: number
  onDaysChange: (days: number) => void
  className?: string
}) {
  const data = usage.daily.map((d) => ({ date: d.date, value: d.in_tokens + d.out_tokens }))
  const total = data.reduce((n, d) => n + d.value, 0)
  return (
    <Panel
      title={`近 ${days} 天模型消耗`}
      meta={`${compactNumber(total)} tokens`}
      className={className}
      action={
        <div className="flex items-center gap-3">
          <RangePills value={days} onChange={onDaysChange} />
          <PanelLink to="/model-calls">模型调用</PanelLink>
        </div>
      }
    >
      <TrendArea
        label="每天的 token 消耗"
        data={data}
        color="var(--cat-sky)"
        format={(v) => `${compactNumber(v)} tokens`}
        axis={compactNumber}
        detail={(i) => {
          const d = usage.daily[i]
          return [
            `${compactNumber(d.in_tokens + d.out_tokens)} tokens（输入 ${compactNumber(d.in_tokens)} · 输出 ${compactNumber(d.out_tokens)}）`,
            `调用 ${d.calls} 次${d.errors > 0 ? ` · 错误 ${d.errors}` : ''}`,
          ]
        }}
      />
    </Panel>
  )
}
