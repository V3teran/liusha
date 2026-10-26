import { useState } from 'react'
import { ChevronRight } from 'lucide-react'
import type { AggregatedFinding } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { severityColor, severityLabel } from '@/lib/severity'
import { findingStatusMeta } from '@/lib/findingStatus'
import { cn } from '@/lib/utils'

interface AggregateCardProps {
  aggregate: AggregatedFinding
  onFindingClick: (findingId: string) => void
}

export function AggregateCard({ aggregate, onFindingClick }: AggregateCardProps) {
  const [expanded, setExpanded] = useState(false)

  const sevVar = (sev: string) => severityColor[sev.toLowerCase()] || 'gray'
  const statusMeta = findingStatusMeta(aggregate.latest_status)

  return (
    <div className="rounded-lg border border-border bg-surface shadow-sm">
      {/* 折叠标题 */}
      <button
        className="w-full p-4 text-left transition-colors hover:bg-surface-2"
        onClick={() => setExpanded(!expanded)}
      >
        <div className="flex items-start gap-4">
          {/* 严重度 */}
          <Badge color={sevVar(aggregate.latest_severity)} size="sm">
            {severityLabel[aggregate.latest_severity.toLowerCase()] || aggregate.latest_severity}
          </Badge>

          {/* 主要信息 */}
          <div className="flex-1 min-w-0">
            <div className="font-semibold text-text">{aggregate.latest_summary}</div>
            <div className="mt-1 text-sm text-text-secondary">
              <span className="font-mono">{aggregate.latest_host}</span>
              {aggregate.latest_target?.path && (
                <>
                  <span className="mx-1.5">·</span>
                  <span className="font-mono">{aggregate.latest_target.path}</span>
                </>
              )}
              {aggregate.latest_cwe_id && (
                <>
                  <span className="mx-1.5">·</span>
                  <span>{aggregate.latest_cwe_id}</span>
                </>
              )}
            </div>
          </div>

          {/* 右侧统计 */}
          <div className="flex items-center gap-6 text-sm">
            {/* 发现次数 */}
            <div className="text-right">
              <div className="text-xs text-text-tertiary">发现次数</div>
              <div className="mt-0.5 text-lg font-bold tabular-nums text-text">
                {aggregate.occurrence_count}
              </div>
            </div>

            {/* 存续时长 */}
            <div className="text-right">
              <div className="text-xs text-text-tertiary">存续天数</div>
              <div className="mt-0.5 text-lg font-bold tabular-nums text-text">
                {aggregate.days_since_first}
              </div>
            </div>

            {/* 最新状态 */}
            <div className="text-right">
              <div className="text-xs text-text-tertiary">最新状态</div>
              <div className="mt-1">
                <Badge color={statusMeta.color} size="sm">
                  {statusMeta.label}
                </Badge>
              </div>
            </div>

            {/* 展开图标 */}
            <ChevronRight
              className={cn(
                'h-5 w-5 flex-shrink-0 text-text-tertiary transition-transform',
                expanded && 'rotate-90'
              )}
            />
          </div>
        </div>
      </button>

      {/* 展开详情 */}
      {expanded && (
        <div className="border-t border-border bg-surface-2 p-4">
          <div className="mb-3 flex items-center justify-between">
            <div className="text-sm font-semibold text-text">历史发现记录</div>
            <div className="text-xs text-text-tertiary">
              首次: {new Date(aggregate.first_seen_at).toLocaleDateString('zh-CN')} ·
              最新: {new Date(aggregate.last_seen_at).toLocaleDateString('zh-CN')}
            </div>
          </div>

          {/* 状态分布 */}
          <div className="mb-4 flex gap-4 rounded-lg bg-surface p-3 text-xs">
            <div className="flex items-center gap-2">
              <span className="text-text-tertiary">待处理</span>
              <span className="font-mono font-semibold tabular-nums text-text">
                {aggregate.status_distribution.open}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-text-tertiary">已确认</span>
              <span className="font-mono font-semibold tabular-nums text-text">
                {aggregate.status_distribution.confirmed}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-text-tertiary">已修复</span>
              <span className="font-mono font-semibold tabular-nums text-text">
                {aggregate.status_distribution.fixed}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-text-tertiary">误报</span>
              <span className="font-mono font-semibold tabular-nums text-text">
                {aggregate.status_distribution.false_positive}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-text-tertiary">已接受</span>
              <span className="font-mono font-semibold tabular-nums text-text">
                {aggregate.status_distribution.accepted}
              </span>
            </div>
          </div>

          {/* Finding 列表 */}
          <div className="space-y-2">
            {aggregate.finding_ids.slice(0, 10).map((id, idx) => (
              <button
                key={id}
                className="flex w-full items-center gap-4 rounded border border-border bg-surface px-3 py-2 text-left text-xs transition-colors hover:border-accent hover:bg-surface-2"
                onClick={(e) => {
                  e.stopPropagation()
                  onFindingClick(id)
                }}
              >
                <span className="font-mono text-text-tertiary">#{aggregate.finding_ids.length - idx}</span>
                <span className="flex-1 font-mono text-text-secondary">{id.slice(0, 8)}...</span>
                <span className="text-accent">查看详情 →</span>
              </button>
            ))}
            {aggregate.finding_ids.length > 10 && (
              <div className="pt-2 text-center text-xs text-text-tertiary">
                还有 {aggregate.finding_ids.length - 10} 条记录未显示
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
