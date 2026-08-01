import { severityTagColor } from '@/lib/severity'

interface FindingCardProps {
  args: string
}

interface FindingArgs {
  summary?: string
  severity?: string
  cwe_id?: string
  owasp_category?: string
  target?: { method?: string; path?: string }
}

function parseArgs(args: string): FindingArgs {
  try {
    return JSON.parse(args || '{}')
  } catch {
    return {}
  }
}

// 漏洞卡：write_finding 的 Result 只有 {id}，真正的字段在 Args（LLM 入参）。
// 解析 args → severity/summary/cwe/target(method+path)，按分级配色高亮。
export function FindingCard({ args }: FindingCardProps) {
  const f = parseArgs(args)
  const sev = (f.severity || 'info').toLowerCase()
  const tag = severityTagColor(sev)

  return (
    <div
      data-card="finding"
      className="flex items-start gap-3 rounded-lg border border-border border-l-[3px] bg-surface px-3.5 py-3"
      style={{ borderLeftColor: tag.textColor }}
    >
      <span
        className="flex-shrink-0 rounded-[5px] border px-2 py-0.5 font-mono text-[11px] font-bold"
        style={{ background: tag.color, color: tag.textColor, borderColor: tag.borderColor }}
      >
        {sev.toUpperCase()}
      </span>
      <div className="min-w-0">
        <div className="text-sm font-medium leading-normal">{f.summary || '(无标题)'}</div>
        <div className="mt-1.5 flex flex-wrap gap-2 text-xs">
          {(f.target?.method || f.target?.path) && (
            <span className="break-all font-mono text-muted">
              <span className="mr-1 font-bold text-accent">{f.target?.method || 'GET'}</span>
              {f.target?.path}
            </span>
          )}
          {f.cwe_id && <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-muted">{f.cwe_id}</span>}
          {f.owasp_category && (
            <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-muted">{f.owasp_category}</span>
          )}
        </div>
      </div>
    </div>
  )
}
