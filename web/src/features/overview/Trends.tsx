import type { OverviewSnapshot } from '@/api/overview'
import { cacheRatePct, compactNumber } from '@/lib/format'
import { SEVERITIES, severityLabel } from '@/lib/severity'
import { DailyBars } from './DailyBars'
import { Panel, PanelLink } from './Panel'
import { Phrases } from './Phrases'

/** 近 N 天每天新增的发现（总数）；各级别多少在当天的明细里。 */
export function FindingsTrend({
  daily,
  days,
  className,
}: {
  daily: OverviewSnapshot['findings']['daily']
  days: number
  className?: string
}) {
  const data = daily.map((d) => ({ date: d.date, value: SEVERITIES.reduce((n, s) => n + (d.counts[s] ?? 0), 0) }))
  const total = data.reduce((n, d) => n + d.value, 0)
  return (
    <Panel
      title={`近 ${days} 天新增发现`}
      meta={`${total} 条`}
      className={className}
      action={<PanelLink to="/findings">发现台账</PanelLink>}
    >
      <DailyBars
        label="每天新增的发现"
        data={data}
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
 * 近 N 天每天的 LLM token 消耗（输入 + 输出），调用次数与错误在当天的明细里；图上方是最近 24 小时的
 * 读数——其中的错误率是 LLM 服务是否正常的信号。
 */
export function UsageTrend({
  usage,
  days,
  className,
}: {
  usage: OverviewSnapshot['usage']
  days: number
  className?: string
}) {
  const data = usage.daily.map((d) => ({ date: d.date, value: d.in_tokens + d.out_tokens }))
  const total = data.reduce((n, d) => n + d.value, 0)
  const last = usage.last_24h
  const errorRate = last.calls > 0 ? (last.errors / last.calls) * 100 : 0
  return (
    <Panel
      title={`近 ${days} 天 LLM 消耗`}
      meta={`${compactNumber(total)} tokens`}
      className={className}
      action={<PanelLink to="/llm-audit">模型日志</PanelLink>}
    >
      <p className="flex flex-wrap items-baseline gap-x-2.5 text-xs text-text-secondary">
        <span className="font-medium text-text">最近 24 小时</span>
        <Phrases
          className="tabular-nums"
          parts={[
            `${compactNumber(last.in_tokens + last.out_tokens)} tokens`,
            `调用 ${last.calls} 次`,
            `错误率 ${errorRate.toFixed(1)}%`,
            `缓存命中 ${cacheRatePct(last.in_tokens, last.cached_tokens).toFixed(1)}%`,
          ]}
        />
      </p>
      <DailyBars
        label="每天的 token 消耗"
        data={data}
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
