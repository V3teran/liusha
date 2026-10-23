import { severityTagColor, severityLabel } from '@/lib/severity'

interface FindingCardProps {
  summary: string
  severity: string
  host?: string
  target?: string
}

// 坐实发现卡：裁决官坐实的主张（晋升 result 节点）经 ClaimSink 落库即发
// finding 事件——领域词中性（信息收集成果与漏洞同为「发现」，severity 的
// info 档即非漏洞成果），分级配色沿用 severity 单一真相源。
export function FindingCard({ summary, severity, host, target }: FindingCardProps) {
  // 类型守卫：severity 应为非空字符串，否则兜底为 'info'
  const rawSev = typeof severity === 'string' && severity.trim() !== '' ? severity : 'info'
  const sev = rawSev.toLowerCase()
  const tag = severityTagColor(sev)

  // 类型守卫：确保 severityLabel[sev] 返回字符串
  const label = severityLabel[sev] || sev

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
              <span className="mr-1 opacity-70">{label}</span>
              {target || host}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}
