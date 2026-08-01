import { useMemo } from 'react'
import { OwnerPicker } from '@/components/OwnerPicker'
import { getSitemap } from '@/api/client'
import type { SitemapNode } from '@/api/types'
import { severityTagColor } from '@/lib/severity'
import { useOwnerResource } from '@/hooks/useOwnerResource'

// 攻击面页：选 owner（仅 active 模式有数据）→ 拉 sitemap 树。
// 渲染 root → domain → endpoint(method+path) → findings(severity tag + summary)。
export function SitemapPage() {
  const { owner, setOwner, data, loading, error, notActive } = useOwnerResource(getSitemap)

  // root.children = domains；每个 domain.children = endpoints。
  const domains = useMemo<SitemapNode[]>(() => data?.root?.children ?? [], [data])
  const endpointCount = useMemo(() => domains.reduce((s, d) => s + (d.children?.length ?? 0), 0), [domains])
  const findingCount = useMemo(
    () => domains.reduce((s, d) => s + (d.children ?? []).reduce((t, e) => t + (e.findings?.length ?? 0), 0), 0),
    [domains],
  )

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center border-b border-border px-5.5 py-3">
        <OwnerPicker value={owner} onChange={setOwner} modeFilter="active" />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto p-5.5">
        {loading ? (
          <div className="py-16 text-center text-[13.5px] text-muted">加载中…</div>
        ) : error ? (
          <div className="py-16 text-center text-[13.5px] text-sev-critical">⚠ {error}</div>
        ) : notActive ? (
          <div className="py-16 text-center text-[13.5px] text-muted">
            攻击面树仅 <b className="text-text">active</b> 模式扫描可用（该 owner 无攻击面数据）
          </div>
        ) : !owner ? (
          <div className="py-16 text-center text-[13.5px] text-muted">请选择一个 active 扫描查看攻击面</div>
        ) : domains.length === 0 ? (
          <div className="py-16 text-center text-[13.5px] text-muted">该扫描暂无攻击面数据</div>
        ) : (
          <>
            <div className="mb-5 grid grid-cols-[repeat(auto-fill,minmax(180px,1fr))] gap-3.5">
              <div className="relative overflow-hidden rounded-xl border border-border bg-surface px-4.5 py-4 shadow">
                <div className="font-mono text-2xl font-bold leading-none">{domains.length}</div>
                <div className="mt-1.5 text-xs text-muted">域名</div>
              </div>
              <div className="relative overflow-hidden rounded-xl border border-border bg-surface px-4.5 py-4 shadow">
                <div className="font-mono text-2xl font-bold leading-none">{endpointCount}</div>
                <div className="mt-1.5 text-xs text-muted">端点</div>
              </div>
              <div className="relative overflow-hidden rounded-xl border border-border bg-surface px-4.5 py-4 shadow">
                <div className="font-mono text-2xl font-bold leading-none">{findingCount}</div>
                <div className="mt-1.5 text-xs text-muted">漏洞</div>
              </div>
            </div>

            {domains.map((domain, di) => (
              <div key={di} className="mb-4 rounded-xl border border-border bg-surface p-4.5 shadow">
                <p className="mb-3.5 flex items-center justify-between text-[13px] font-semibold text-muted">
                  <span className="font-mono">{domain.name}</span>
                  <span className="font-normal text-muted">{domain.children?.length ?? 0} 端点</span>
                </p>
                {(domain.children ?? []).map((ep, ei) => (
                  <div key={ei} className="border-b border-border py-2 last:border-b-0">
                    <div className="flex items-center gap-2.5">
                      <span className="min-w-11 font-mono text-[11px] font-bold text-accent">{ep.method || 'GET'}</span>
                      <span className="break-all text-[13px] text-text">{ep.path || ep.name}</span>
                    </div>
                    {(ep.findings?.length ?? 0) > 0 && (
                      <div className="ml-[54px] mt-2 flex flex-col gap-1.5">
                        {ep.findings!.map((f) => {
                          const tag = severityTagColor(f.severity)
                          return (
                            <div key={f.id} className="flex items-center gap-2.5">
                              <span
                                className="rounded border px-1.5 py-0.5 text-[11px] font-semibold"
                                style={{ color: tag.textColor, background: tag.color, borderColor: tag.borderColor }}
                              >
                                {f.severity}
                              </span>
                              <span className="text-[13px]">{f.summary}</span>
                              {f.cwe_id && <span className="font-mono text-[11px] text-muted">{f.cwe_id}</span>}
                            </div>
                          )
                        })}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            ))}
          </>
        )}
      </div>
    </div>
  )
}
