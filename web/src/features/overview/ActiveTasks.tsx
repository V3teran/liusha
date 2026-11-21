import { Link } from 'react-router-dom'
import type { TaskSummary } from '@/api/types'
import { jumpContext, taskJumps } from '@/features/relations/taskJumps'
import { relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Panel, PanelEmpty, PanelLink } from './Panel'

/** 进行中的任务：最新的几条，每条显示目标、所处状态与进度；总数在关键数字里。 */
export function ActiveTasks({
  tasks,
  total,
  className,
}: {
  tasks: TaskSummary[]
  total: number
  className?: string
}) {
  return (
    <Panel
      title="进行中的任务"
      meta={String(total)}
      className={className}
      action={total > tasks.length ? <PanelLink to="/tasks?status=active">全部</PanelLink> : undefined}
    >
      {tasks.length === 0 ? (
        <PanelEmpty>暂无进行中的任务，可在「任务」或「对话」里下发。</PanelEmpty>
      ) : (
        <ul className="-my-1 flex flex-col divide-y divide-border-subtle">
          {tasks.map((t) => {
            const to = taskJumps(jumpContext(t, 'task'), ['task'])[0]?.to ?? '/tasks'
            return (
              <li key={t.id}>
                <Link to={to} className="-mx-2 flex min-w-0 items-start gap-2.5 rounded-md px-2 py-2.5 transition-colors hover:bg-surface-2">
                  <span
                    aria-hidden
                    className={cn('mt-1.5 h-1.5 w-1.5 flex-shrink-0 rounded-full', t.paused ? 'bg-status-neutral' : 'bg-status-success')}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm text-text" title={t.goal}>
                      {t.goal}
                    </span>
                    <span className="mt-0.5 flex flex-wrap gap-x-2.5 text-xs tabular-nums text-text-secondary">
                      <span>{t.paused ? '已暂停' : '运行中'}</span>
                      <span>第 {t.step_count} 步</span>
                      {t.finding_count > 0 && <span>发现 {t.finding_count}</span>}
                      <span>{relativeTime(t.created_at)}开始</span>
                    </span>
                  </span>
                </Link>
              </li>
            )
          })}
        </ul>
      )}
    </Panel>
  )
}
