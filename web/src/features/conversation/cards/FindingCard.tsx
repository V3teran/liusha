import { severityTagColor, severityLabel } from '@/lib/severity'

interface FindingCardProps {
  summary: string
  severity: string
  host?: string
  target?: string
}

// 漏洞卡：裁决官坐实的漏洞在时间线单独成卡（evaluator 经 ClaimSink 落库即发
// finding 事件）——漏洞是任务的核心产出，不能只藏在完成节点的计数里。
// 分级配色沿用 severity 单一真相源。
export function FindingCard({ summary, severity, host, target }: FindingCardProps) {
  const sev = (severity || 'info').toLowerCase()
  const tag = severityTagColor(sev)

  return (
    <div
      data-card="finding"
      className="flex items-start gap-3 rounded-[10px] border border-border border-l-[3px] bg-surface px-3.5 py-3"
      style={{ borderLeftColor: tag.textColor }}
    >
      <span
        className="flex-shrink-0 rounded-md border px-2 py-0.5 font-mono text-[11px] font-bold"
        style={{ background: tag.color, color: tag.textColor, borderColor: tag.borderColor }}
      >
        {sev.toUpperCase()}
      </span>
      <div className="min-w-0">
        <div className="text-sm font-medium leading-normal">{summary || '(无标题)'}</div>
        <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted">
          {(host || target) && (
            <span className="break-all font-mono">
              <span className="mr-1 opacity-70">{severityLabel[sev] || sev}</span>
              {target || host}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}
