import { Link } from 'react-router-dom'
import type { SeverityCounts } from '@/api/overview'
import { SEVERITIES, severityColorOf, severityLabel } from '@/lib/severity'
import { Panel, PanelEmpty, PanelLink } from './Panel'

/**
 * 待处理发现：「有多少问题等着处理」是概览最该回答的问题，总数用最大字号。分段条给各严重级的
 * 比例，下方逐级列出数量（级别名是文字，颜色只作辅助），点一级进入按「待处理 + 该级别」筛好的台账。
 */
export function OpenFindings({ open, className }: { open: SeverityCounts; className?: string }) {
  const parts = SEVERITIES.map((sev) => ({ sev, n: open[sev] ?? 0 })).filter((p) => p.n > 0)
  const total = parts.reduce((n, p) => n + p.n, 0)
  return (
    <Panel title="待处理发现" className={className} action={<PanelLink to="/findings?status=open">全部待处理</PanelLink>}>
      {total === 0 ? (
        <PanelEmpty>没有待处理的发现。</PanelEmpty>
      ) : (
        <div className="flex flex-col gap-3">
          <p className="text-2xl font-semibold leading-none tabular-nums text-text">{total}</p>
          <span aria-hidden className="flex h-2 gap-0.5 overflow-hidden rounded-full">
            {parts.map((p) => (
              <span key={p.sev} className="h-full min-w-0.5 basis-0" style={{ flexGrow: p.n, background: severityColorOf(p.sev) }} />
            ))}
          </span>
          <ul className="-mx-2 flex flex-col">
            {parts.map((p) => (
              <li key={p.sev}>
                <Link
                  to={`/findings?status=open&severity=${p.sev}`}
                  aria-label={`${severityLabel[p.sev]} ${p.n} 条`}
                  className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm transition-colors hover:bg-surface-2 focus-visible:bg-surface-2"
                >
                  <span aria-hidden className="h-2 w-2 rounded-sm" style={{ background: severityColorOf(p.sev) }} />
                  <span className="flex-1 text-text-secondary">{severityLabel[p.sev]}</span>
                  <span className="tabular-nums text-text">{p.n}</span>
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Panel>
  )
}
