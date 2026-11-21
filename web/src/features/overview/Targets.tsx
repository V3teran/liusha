import { Link } from 'react-router-dom'
import type { OverviewTarget } from '@/api/overview'
import { relativeTime } from '@/lib/format'
import { SEVERITIES, severityColorOf, severityLabel } from '@/lib/severity'
import { Panel, PanelEmpty } from './Panel'

const LINK = 'rounded text-xs text-text-secondary underline-offset-2 transition-colors hover:text-accent hover:underline'

/**
 * 目标：按任务瞄准的主机汇总——待处理的严重级分布、发现总数、任务数与最近活动。
 * 待处理越重的排越前，回答「哪个目标最需要先看」。
 */
export function Targets({ targets, className }: { targets: OverviewTarget[]; className?: string }) {
  return (
    <Panel title="目标" className={className}>
      {targets.length === 0 ? (
        <PanelEmpty>下发带目标地址的任务后，这里按目标汇总任务与发现。</PanelEmpty>
      ) : (
        <div role="region" aria-label="目标列表" tabIndex={0} className="-mx-4 -my-3 overflow-x-auto px-4 sm:-mx-5 sm:px-5">
          <table className="w-full min-w-[560px] border-collapse text-sm">
            <thead>
              <tr className="border-b border-border-subtle text-left text-xs font-medium text-text-tertiary">
                <th scope="col" className="py-2 pr-4">目标</th>
                <th scope="col" className="py-2 pr-4">待处理</th>
                <th scope="col" className="py-2 pr-4 text-right">发现</th>
                <th scope="col" className="py-2 pr-4 text-right">任务</th>
                <th scope="col" className="py-2 pr-4">最近活动</th>
                <th scope="col" className="relative py-2 text-right">
                  <span className="sr-only">去处</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {targets.map((t) => {
                const q = encodeURIComponent(t.host)
                const open = SEVERITIES.filter((s) => (t.open[s] ?? 0) > 0)
                return (
                  <tr key={t.host} className="border-b border-border-subtle last:border-b-0">
                    <td className="max-w-[16rem] truncate py-2.5 pr-4 font-mono text-xs text-text" title={t.host}>
                      {t.host}
                    </td>
                    <td className="py-2.5 pr-4">
                      {open.length === 0 ? (
                        <span className="text-xs text-text-tertiary">无</span>
                      ) : (
                        <span className="flex flex-wrap gap-x-3 text-xs tabular-nums text-text-secondary">
                          {open.map((s) => (
                            <span key={s} className="inline-flex items-center gap-1">
                              <span aria-hidden className="h-2 w-2 rounded-sm" style={{ background: severityColorOf(s) }} />
                              {severityLabel[s]} {t.open[s]}
                            </span>
                          ))}
                        </span>
                      )}
                    </td>
                    <td className="py-2.5 pr-4 text-right tabular-nums text-text">{t.findings}</td>
                    <td className="py-2.5 pr-4 text-right tabular-nums text-text">
                      {t.tasks}
                      {t.active_tasks > 0 && (
                        <span className="ml-1.5 text-xs text-status-success">{t.active_tasks} 进行中</span>
                      )}
                    </td>
                    <td className="py-2.5 pr-4 text-xs text-text-secondary">{relativeTime(t.last_activity_at)}</td>
                    <td className="py-2.5 text-right">
                      <span className="inline-flex gap-3 whitespace-nowrap">
                        <Link to={`/tasks?search=${q}`} className={LINK}>
                          任务
                        </Link>
                        {t.findings > 0 && (
                          <Link to={`/findings?search=${q}`} className={LINK}>
                            发现
                          </Link>
                        )}
                      </span>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  )
}
