import { Link } from 'react-router-dom'
import { Bug, Clock, GitBranch, MessageSquare, X } from 'lucide-react'
import type { TaskSummary } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { fullTime, humanDuration } from '@/lib/format'
import { scanStatusMeta } from '@/lib/scanStatus'

interface TaskDetailPaneProps {
  task: TaskSummary | null
  loading: boolean
  error: string
  onClose: () => void
}

function Kv({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[84px_1fr] gap-x-3 gap-y-1.5 py-0.5 text-sm">
      <dt className="text-muted">{k}</dt>
      <dd className="min-w-0 break-all text-text">{children}</dd>
    </div>
  )
}

/** 任务详情板（右侧常驻，点行选中驱动）：任务信息 KV + 目标全文 + 三跳转。 */
export function TaskDetailPane({ task, loading, error, onClose }: TaskDetailPaneProps) {
  return (
    <div className="flex h-full min-h-0 flex-col border-l border-border bg-surface">
      <div className="flex items-center gap-2.5 border-b border-border px-4 py-3">
        <span className="min-w-0 flex-1 truncate text-[13.5px] font-semibold text-text" title={task?.brief}>
          {task ? task.brief : '任务详情'}
        </span>
        <button
          type="button"
          onClick={onClose}
          aria-label="关闭详情"
          className="rounded-md px-1.5 py-0.5 text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto px-4 py-4">
        {error ? (
          <div className="py-10 text-center text-sm text-sev-critical">⚠ {error}</div>
        ) : loading && !task ? (
          <div className="space-y-3">
            {Array.from({ length: 6 }).map((_, i) => (
              <span key={i} className="block h-3 animate-pulse rounded bg-surface-2" />
            ))}
          </div>
        ) : task ? (
          <div className="flex flex-col gap-5">
            <section>
              <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-muted">任务信息</h4>
              <dl>
                <Kv k="状态">
                  <Badge dot color={scanStatusMeta(task.status).color}>
                    {task.status === 'active' ? '运行中' : scanStatusMeta(task.status).label}
                  </Badge>
                </Kv>
                <Kv k="来源">
                  <span className="inline-flex items-center gap-1.5">
                    <Badge dot={false} color={task.source === 'manual' ? 'var(--source-manual)' : 'var(--source-auto)'}>
                      {task.source === 'manual' ? '主动' : '被动'}
                    </Badge>
                    {task.scheduled && (
                      <span className="inline-flex items-center gap-1 text-xs text-text-tertiary" title={`定时触发 · ${task.cron}`}>
                        <Clock className="h-3 w-3" />定时
                      </span>
                    )}
                  </span>
                </Kv>
                <Kv k="批次ID">
                  <span className="font-mono text-xs" title={task.assignment_id}>
                    {task.assignment_id.slice(0, 8)}（第 {task.assignment_ndx} / {task.assignment_total} 个）
                  </span>
                </Kv>
                <Kv k="进度">
                  <span className="font-mono text-xs">
                    {task.step_count} 步{task.status === 'active' ? ' · 进行中' : ''}
                  </span>
                </Kv>
                <Kv k="创建时间">
                  <span className="font-mono text-xs" title={fullTime(task.created_at)}>
                    {fullTime(task.created_at)}
                  </span>
                </Kv>
                <Kv k="结束时间">
                  <span className="font-mono text-xs" title={task.ended_at ? fullTime(task.ended_at) : undefined}>
                    {task.ended_at ? fullTime(task.ended_at) : '—'}
                  </span>
                </Kv>
                <Kv k="耗时">
                  <span className="font-mono text-xs">
                    {task.ended_at
                      ? humanDuration(new Date(task.ended_at).getTime() - new Date(task.created_at).getTime())
                      : '—'}
                  </span>
                </Kv>
                <Kv k="漏洞">
                  <span className={task.finding_count > 0 ? 'font-semibold text-sev-high' : 'text-text'}>
                    {task.finding_count} 个
                  </span>
                </Kv>
              </dl>
            </section>

            <section>
              <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-muted">目标</h4>
              <div className="rounded-lg border border-border bg-surface-2 px-3 py-2.5 text-[12.5px] leading-relaxed text-text-secondary">
                {task.brief}
              </div>
            </section>

            <section>
              <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-muted">跳转</h4>
              <div className="flex flex-wrap gap-2">
                {task.conversation_id && (
                  <Link
                    to={`/conversations/${task.source === 'manual' ? 'manual' : 'auto'}?conv=${encodeURIComponent(task.conversation_id)}`}
                    className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-[12.5px] text-text-secondary transition-colors hover:border-accent hover:text-accent"
                  >
                    <MessageSquare className="h-3.5 w-3.5" />对话
                  </Link>
                )}
                {task.conversation_id && (
                  <Link
                    to={`/exploration-graph?conversation_id=${encodeURIComponent(task.conversation_id)}`}
                    className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-[12.5px] text-text-secondary transition-colors hover:border-accent hover:text-accent"
                  >
                    <GitBranch className="h-3.5 w-3.5" />探索图
                  </Link>
                )}
                <Link
                  to="/findings"
                  className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-[12.5px] text-text-secondary transition-colors hover:border-accent hover:text-accent"
                >
                  <Bug className="h-3.5 w-3.5" />漏洞
                </Link>
              </div>
            </section>
          </div>
        ) : null}
      </div>
    </div>
  )
}
