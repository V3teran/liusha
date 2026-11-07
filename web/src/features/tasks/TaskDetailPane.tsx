import { Bug, Clock, GitBranch, MessageSquare, Pause, Play, Square, X } from 'lucide-react'
import { controlTask } from '@/api/tasks'
import { JumpLink } from '@/components/JumpLink'
import type { TaskSummary } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { fullTime, humanDuration } from '@/lib/format'
import { taskStatusMeta } from '@/lib/taskStatus'

interface TaskDetailPaneProps {
  task: TaskSummary | null
  loading: boolean
  error: string
  onClose: () => void
  /** 控制操作（暂停/继续/停止）完成后的刷新回调。 */
  onOperated?: () => void
}

function Kv({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[84px_1fr] gap-x-3 gap-y-1.5 py-0.5 text-sm">
      <dt className="text-text-secondary">{k}</dt>
      <dd className="min-w-0 break-all text-text">{children}</dd>
    </div>
  )
}

/** 任务详情板（右侧常驻，点行选中驱动）：任务信息 KV + 目标全文 + 控制组 + 三跳转。
 * 控制组与表格操作列/对话页头部同一端点、同一语义——详情是检查面，命中区域更大，
 * 表格行内是快捷路径，两面并存。 */
export function TaskDetailPane({ task, loading, error, onClose, onOperated }: TaskDetailPaneProps) {
  const control = async (action: 'pause' | 'resume' | 'terminate') => {
    if (!task) return
    if (action === 'terminate') {
      const head = task.brief.length > 24 ? `${task.brief.slice(0, 24)}…` : task.brief
      if (!window.confirm(`停止任务「${head}」？`)) return
    }
    try {
      await controlTask(task.id, action)
    } catch (e) {
      window.alert(`操作失败：${(e as Error).message}`)
      return
    }
    onOperated?.()
  }

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
          <div className="py-10 text-center text-sm text-status-error">⚠ {error}</div>
        ) : loading && !task ? (
          <div className="space-y-3">
            {Array.from({ length: 6 }).map((_, i) => (
              <span key={i} className="block h-3 animate-pulse rounded bg-surface-2" />
            ))}
          </div>
        ) : task ? (
          <div className="flex flex-col gap-5">
            <section>
              <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-text-secondary">任务信息</h4>
              <dl>
                <Kv k="状态">
                  <Badge dot color={taskStatusMeta(task.status).color}>
                    {task.status === 'active' ? (task.paused ? '已暂停' : '运行中') : taskStatusMeta(task.status).label}
                  </Badge>
                </Kv>
                <Kv k="来源">
                  <span className="inline-flex items-center gap-1.5">
                    <Badge dot={false} color={task.source === 'self' ? 'var(--source-self)' : 'var(--source-proxy)'}>
                      {task.source === 'self' ? '自主' : '代理'}
                    </Badge>
                    {task.scheduled && (
                      <span className="inline-flex items-center gap-1 text-xs text-text-tertiary" title={`定时触发 · ${task.cron}`}>
                        <Clock className="h-3 w-3" />定时
                      </span>
                    )}
                  </span>
                </Kv>
                <Kv k="批次">
                  <span className="text-xs">
                    {task.assignment_title}（第 {task.assignment_index} / {task.assignment_total} 个）
                  </span>
                </Kv>
                <Kv k="进度">
                  <span className="font-mono text-xs">
                    {task.step_count} 步{task.status === 'active' ? (task.paused ? ' · 已暂停' : ' · 进行中') : ''}
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

              {/* 控制组恒显按状态禁用（与表格操作列同一行为）：终态行三键灰置 */}
              <div className="mt-3.5 flex gap-2">
                <button
                  type="button"
                  disabled={task.status !== 'active' || task.paused}
                  onClick={() => void control('pause')}
                  title={task.status !== 'active' ? '任务已结束' : task.paused ? '已暂停' : '暂停（保留进度，可随时继续）'}
                  className={`inline-flex flex-1 items-center justify-center gap-1.5 rounded-lg border px-3 py-1.5 text-[12.5px] transition-colors ${
                    task.status !== 'active' || task.paused
                      ? 'cursor-not-allowed border-border text-text-tertiary opacity-40'
                      : 'border-border text-text-secondary hover:bg-surface-2 hover:text-text'
                  }`}
                >
                  <Pause className="h-3.5 w-3.5" />
                  暂停任务
                </button>
                <button
                  type="button"
                  disabled={task.status !== 'active' || !task.paused}
                  onClick={() => void control('resume')}
                  title={task.status !== 'active' ? '任务已结束' : task.paused ? '继续（从探索图进度恢复执行）' : '运行中'}
                  className={`inline-flex flex-1 items-center justify-center gap-1.5 rounded-lg border px-3 py-1.5 text-[12.5px] transition-colors ${
                    task.status !== 'active' || !task.paused
                      ? 'cursor-not-allowed border-border text-text-tertiary opacity-40'
                      : 'border-border text-text-secondary hover:bg-surface-2 hover:text-text'
                  }`}
                >
                  <Play className="h-3.5 w-3.5" />
                  继续任务
                </button>
                <button
                  type="button"
                  disabled={task.status !== 'active'}
                  onClick={() => void control('terminate')}
                  title={task.status !== 'active' ? '任务已结束' : '停止（终态，可重新下发）'}
                  className={`inline-flex flex-1 items-center justify-center gap-1.5 rounded-lg border px-3 py-1.5 text-[12.5px] font-medium transition-colors ${
                    task.status !== 'active'
                      ? 'cursor-not-allowed border-status-error/40 bg-status-error/5 text-status-error/40'
                      : 'border-status-error bg-status-error/10 text-status-error hover:bg-status-error/20'
                  }`}
                >
                  <Square className="h-3.5 w-3.5 fill-current" />
                  停止任务
                </button>
              </div>
            </section>

            <section>
              <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-text-secondary">目标</h4>
              <div className="rounded-lg border border-border bg-surface-2 px-3 py-2.5 text-[12.5px] leading-relaxed text-text-secondary">
                {task.brief}
              </div>
            </section>

            {Boolean(task.conversation_id || task.finding_count > 0) && (
              <section>
                <h4 className="mb-2 text-[11px] font-semibold uppercase tracking-wider text-text-secondary">跳转</h4>
                <div className="flex flex-wrap gap-2">
                  {task.conversation_id && (
                    <JumpLink
                      to={`/conversations/${task.source === 'self' ? 'direct' : 'proxy'}?conv=${encodeURIComponent(task.conversation_id)}`}
                      icon={MessageSquare}
                    >
                      对话
                    </JumpLink>
                  )}
                  {task.conversation_id && (
                    <JumpLink
                      to={`/exploration-graph?conversation_id=${encodeURIComponent(task.conversation_id)}`}
                      icon={GitBranch}
                    >
                      探索图
                    </JumpLink>
                  )}
                  {/* 无洞不跳：按钮必须通向有内容的落点，空台账链接是噪音 */}
                  {task.finding_count > 0 && (
                    <JumpLink to="/findings" icon={Bug}>
                      漏洞
                    </JumpLink>
                  )}
                </div>
              </section>
            )}
          </div>
        ) : null}
      </div>
    </div>
  )
}
