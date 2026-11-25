import { Fragment } from 'react'
import { Badge } from '@/components/ui/Badge'
import { SEVERITIES, severityColorOf, severityEntries, severityLabel, severityTextColor } from '@/lib/severity'

type Counts = Readonly<Record<string, number | undefined>>

/** 带档名的完整说法（严重 1 / 高危 3 / …）：悬停提示与读屏用。 */
function spelledOut(counts: Counts): string {
  return SEVERITIES.map((sev) => `${severityLabel[sev]} ${counts[sev] ?? 0}`).join(' / ')
}

/**
 * 按严重度分档的发现数，列表单元格用的紧凑形态：五个数固定按 严重 / 高危 / 中危 / 低危 / 信息 排列，
 * 用斜线隔开（如 1/3/2/1/1），每个数用它那一档的颜色；为 0 的档照样占位，只是不着色——有发现的那几档
 * 一眼就看得出。位置本身就是档名：表头的悬停提示写出这个顺序，格子的悬停提示与读屏给出带档名的说法。
 */
export function SeverityTally({ counts }: { counts: Counts }) {
  return (
    <span className="whitespace-nowrap text-xs tabular-nums" title={spelledOut(counts)}>
      {SEVERITIES.map((sev, i) => {
        const n = counts[sev] ?? 0
        return (
          <Fragment key={sev}>
            {i > 0 && <span className="text-text-tertiary">/</span>}
            <span className={n > 0 ? 'font-semibold' : 'text-text-tertiary'} style={n > 0 ? { color: severityTextColor(sev) } : undefined}>
              {n}
            </span>
          </Fragment>
        )
      })}
      <span className="sr-only">（{spelledOut(counts)}）</span>
    </span>
  )
}

/**
 * 带档名的气泡形态（任务详情用）：一档一枚徽章，与发现台账的严重度徽章同一套色；为 0 的档不列。
 * 一个发现都没有时留「—」。
 */
export function SeverityBadges({ counts }: { counts: Counts }) {
  const entries = severityEntries(counts)
  if (entries.length === 0) return <span className="text-text-tertiary">—</span>
  return (
    <span className="flex flex-wrap gap-1.5">
      {entries.map(([sev, n]) => (
        <Badge key={sev} dot={false} size="sm" color={severityColorOf(sev)} className="tabular-nums">
          {severityLabel[sev]} {n}
        </Badge>
      ))}
    </span>
  )
}
