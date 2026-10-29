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
//
// 严重度三重冗余编码（左色条 + 中文徽章同色底 + 徽章同色字）保证一眼分级；
// 徽章用中文（严重/高危/中危/低危/信息）与全站一致；目标行显式标注「目标」，
// 不再把严重度中文标签错当 host 前缀。
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
      className="rounded-xl border border-l-[3px] bg-surface px-4 py-3"
      style={{
        // 全卡底色 = 严重度色低透明铺底（业界告警卡惯例：整卡可依颜色一眼分级），
        // 左色条 3px 同色强化。tag.color = 严重度色 13% alpha，暗浅主题都可读。
        background: `linear-gradient(to right, ${tag.textColor}1f, ${tag.textColor}0d), var(--surface)`,
        borderColor: 'var(--border)',
        borderLeftColor: tag.textColor,
      }}
    >
      <div className="flex items-start gap-2.5">
        <span
          className="flex-shrink-0 rounded-md border px-2 py-0.5 text-xs font-bold"
          style={{ background: tag.color, color: tag.textColor, borderColor: tag.borderColor }}
        >
          {label}
        </span>
        <div className="min-w-0 flex-1 text-sm font-medium leading-normal">{summary || '(无标题)'}</div>
      </div>
      {(host || target) && (
        <div className="mt-2 flex items-baseline gap-1.5 text-xs">
          <span className="flex-shrink-0 text-muted">目标</span>
          <span className="break-all font-mono text-text-secondary">{target || host}</span>
        </div>
      )}
    </div>
  )
}
