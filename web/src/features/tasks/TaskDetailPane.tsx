import { useState } from 'react'
import { Button } from '@/components/ui/Button'
import { TokenPair } from '@/components/TokenPair'
import { Clock, ListPlus, Pause, PencilLine, Play, Square, X } from 'lucide-react'
import { controlTask } from '@/api/tasks'
import { JumpLinks } from '@/features/relations/TaskJumpLinks'
import { jumpContext, taskJumps } from '@/features/relations/taskJumps'
import { useTaskUsage } from './useTaskQueries'
import type { RoleUsage, TaskSummary } from '@/api/types'
import { SeverityBadges } from '@/features/finding/SeverityCounts'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { agentAccent, agentLabel, KNOWN_AGENTS } from '@/lib/agentColor'
import { clockDuration, fullTime, humanDuration } from '@/lib/format'
import { taskStatusMeta } from '@/lib/taskStatus'
import { sourceLabel } from '@/lib/source'
import { goalHead } from './taskColumns'
import { AdjustGoalDialog, AssignActionDialog } from './SteeringDialogs'
import { ErrorState } from '@/components/ui/ErrorState'
import { toast } from '@/components/ui/toast'

interface TaskDetailPaneProps {
  task: TaskSummary | null
  loading: boolean
  error: string
  onRetry: () => void
  onClose: () => void
  /** 控制操作（暂停/继续/停止）完成后的刷新回调。 */
  onOperated?: () => void
  /** 打开来源定时模板（「定时」标记点击）；不传时标记仅展示。 */
  onOpenSchedule?: (scheduleId: string) => void
}

function Kv({ k, children }: { k: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[84px_1fr] gap-x-3 gap-y-1.5 py-0.5 text-sm">
      <dt className="text-text-secondary">{k}</dt>
      <dd className="min-w-0 break-all text-text">{children}</dd>
    </div>
  )
}

/** 智能体调用情况的先后：登记过的身份按登记顺序在前（规划者、执行者、评估者、监察者），其余（历史压缩等）照接口给的先后。 */
function rolesInOrder(roles: RoleUsage[]): RoleUsage[] {
  const rank = (role: string) => {
    const i = KNOWN_AGENTS.indexOf(role)
    return i === -1 ? KNOWN_AGENTS.length : i
  }
  return [...roles].sort((a, b) => rank(a.role) - rank(b.role))
}

/**
 * 任务详情板（右侧常驻，点行选中驱动）：跳转 + 任务信息 KV + 控制组 + 目标全文。
 * 控制组与表格操作列/对话页头部同一端点、同一语义——详情是检查面，命中区域更大，
 * 表格行内是快捷路径，两面并存。
 *
 * 这里一次只看一个任务，信息以纯文字为主：状态、来源是带颜色点的文字（列表里用徽章是为了在行与行
 * 之间扫读，详情里没有必要）；只有发现数用气泡——五个严重度的颜色本身就是信息。
 */
export function TaskDetailPane({ task, loading, error, onRetry, onClose, onOperated, onOpenSchedule }: TaskDetailPaneProps) {
  // 暂停/继续合并为随 paused 翻转的切换钮（可逆，直接执行）；
  // 只有不可逆的停止过确认弹层（danger 形态）。
  const [confirmTerminate, setConfirmTerminate] = useState(false)
  // 运行中引导（调整目标 / 指派动作）：只对进行中（含暂停中）的任务开放。
  const [steering, setSteering] = useState<'goal' | 'action' | null>(null)
  const doControl = async (action: 'pause' | 'resume' | 'terminate') => {
    if (!task) return
    try {
      await controlTask(task.id, action)
    } catch (e) {
      toast.error(`操作失败：${(e as Error).message}`)
      return
    }
    onOperated?.()
  }
  const control = async (action: 'pause' | 'resume' | 'terminate') => {
    if (!task) return
    if (action === 'terminate') {
      setConfirmTerminate(true)
      return
    }
    await doControl(action)
  }
  // 用量（智能体调用情况、耗时）：任务进行中每 10 秒跟着刷新
  const usage = useTaskUsage(task?.id ?? null, task?.status === 'active').data

  return (
    <div className="flex h-full min-h-0 flex-col border-l border-border bg-surface">
      <div className="flex items-center gap-2.5 border-b border-border px-4 py-3">
        <span className="min-w-0 flex-1 truncate text-sm font-semibold text-text" title={task?.goal}>
          {task ? task.goal : '任务详情'}
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
          <ErrorState message={error} onRetry={onRetry} />
        ) : loading && !task ? (
          <div className="space-y-3">
            {Array.from({ length: 6 }).map((_, i) => (
              <span key={i} className="block h-3 animate-pulse rounded bg-surface-2" />
            ))}
          </div>
        ) : task ? (
          <div className="flex flex-col gap-5">
            {/* 跳转排在首位：从任务去对话、发现、探索图与模型日志是最先用到的出口（只看这个任务的）。 */}
            <section>
              <h4 className="mb-2 text-2xs font-semibold uppercase tracking-wider text-text-secondary">跳转</h4>
              <JumpLinks jumps={taskJumps(jumpContext(task, 'task'), ['conversation', 'findings', 'exploration', 'llmAudit'])} />
            </section>
            <section>
              <h4 className="mb-2 text-2xs font-semibold uppercase tracking-wider text-text-secondary">任务信息</h4>
              <dl>
                <Kv k="状态">
                  <span className="inline-flex items-center gap-1.5 text-xs">
                    <span aria-hidden className="h-2 w-2 rounded-full" style={{ background: taskStatusMeta(task.status).color }} />
                    {task.status === 'active' ? (task.paused ? '已暂停' : '运行中') : taskStatusMeta(task.status).label}
                  </span>
                </Kv>
                {task.status === 'aborted' && task.abort_reason && (
                  <Kv k="终止原因">
                    <span className="text-xs">{task.abort_reason}</span>
                  </Kv>
                )}
                <Kv k="来源">
                  <span className="inline-flex items-center gap-1.5 text-xs">
                    <span>{sourceLabel(task.source)}</span>
                    {task.scheduled &&
                      (onOpenSchedule ? (
                        <button
                          type="button"
                          onClick={() => onOpenSchedule(task.schedule_id)}
                          title={`定时触发 · ${task.cron}`}
                          className="inline-flex cursor-pointer items-center gap-1 rounded text-xs text-text-tertiary underline-offset-2 transition-colors hover:text-accent hover:underline"
                        >
                          <Clock className="h-3 w-3" aria-hidden />定时 · 查看模板
                        </button>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-xs text-text-tertiary" title={`定时触发 · ${task.cron}`}>
                          <Clock className="h-3 w-3" aria-hidden />定时
                        </span>
                      ))}
                  </span>
                </Kv>
                <Kv k="场景">
                  <span className="text-xs">
                    {task.scenario ? task.scenario_name : '无场景'}
                    {task.scenario_deleted && <span className="ml-1 text-text-tertiary">（已删除）</span>}
                  </span>
                </Kv>
                <Kv k="批次">
                  <span className="text-xs">
                    {task.assignment_title}（第 {task.assignment_index} / {task.assignment_total} 个）
                  </span>
                </Kv>
                {/* 智能体调用情况：谁调了多少次模型、多少次工具、各花了多少 token（纯文字，一行一个智能体） */}
                <Kv k="智能体调用">
                  {!usage ? (
                    <span className="text-xs text-text-tertiary">加载中…</span>
                  ) : usage.by_role.length === 0 ? (
                    <span className="text-xs text-text-tertiary">—</span>
                  ) : (
                    <ul className="flex flex-col gap-0.5">
                      {rolesInOrder(usage.by_role).map((r) => (
                        <li key={r.role} className="flex flex-wrap items-center gap-x-2 text-xs">
                          <span className="inline-flex w-12 shrink-0 items-center gap-1.5">
                            <span aria-hidden className="h-2 w-2 rounded-full" style={{ background: agentAccent(r.role).accent }} />
                            <span className="truncate">{agentLabel(r.role) || r.role}</span>
                          </span>
                          <span className="text-text-secondary">
                            调用 {r.llm_calls} 次 · 工具 {r.tool_calls} 次
                          </span>
                          {(r.tokens.in > 0 || r.tokens.out > 0) && (
                            <TokenPair inTokens={r.tokens.in} outTokens={r.tokens.out} cachedTokens={r.tokens.cached} />
                          )}
                        </li>
                      ))}
                    </ul>
                  )}
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
                    {/* 与对话页顶栏同一个口径、同一种写法（扣除续接前的停顿）；用量还没回来时先按起止时间估 */}
                    {usage
                      ? clockDuration(usage.elapsed_ms)
                      : task.ended_at
                        ? humanDuration(new Date(task.ended_at).getTime() - new Date(task.created_at).getTime())
                        : '—'}
                  </span>
                </Kv>
                <Kv k="发现">
                  <SeverityBadges counts={task.findings_by_severity} />
                </Kv>
              </dl>

              {/* 详情是检查面：控制组恒显、任务结束后禁用并在下方说明原因（表格操作列只在进行中出现）。
                  暂停/继续合并为随 paused 翻转的单切换钮，停止独立。 */}
              <div className="mt-3.5 flex gap-2">
                <Button
                  variant="secondary"
                  size="sm"
                  className="flex-1"
                  disabled={task.status !== 'active'}
                  onClick={() => void control(task.paused ? 'resume' : 'pause')}
                  title={task.status !== 'active' ? '任务已结束' : task.paused ? '继续（从探索图进度恢复执行）' : '暂停（保留进度，可随时继续）'}
                >
                  {task.paused ? <Play /> : <Pause />}
                  {task.paused ? '继续任务' : '暂停任务'}
                </Button>
                <Button
                  variant="danger"
                  size="sm"
                  className="flex-1"
                  disabled={task.status !== 'active'}
                  onClick={() => void control('terminate')}
                  title={task.status !== 'active' ? '任务已结束' : '停止（终态，可重新下发）'}
                >
                  <Square className="fill-current" />
                  停止任务
                </Button>
              </div>
            </section>

            <section aria-labelledby="task-goal-heading">
              <h4 id="task-goal-heading" className="mb-2 text-2xs font-semibold uppercase tracking-wider text-text-secondary">
                目标
              </h4>
              <div className="whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-2 px-3 py-2.5 text-xs leading-relaxed text-text-secondary">
                {task.goal}
              </div>
              {/* 运行中引导：修正往哪跑（与上方控制组「跑不跑」分开）。已结束的任务在对话里续接。 */}
              <div className="mt-2.5 flex gap-2">
                <Button
                  variant="secondary"
                  size="sm"
                  className="flex-1"
                  disabled={task.status !== 'active'}
                  onClick={() => setSteering('goal')}
                >
                  <PencilLine />
                  调整目标
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  className="flex-1"
                  disabled={task.status !== 'active'}
                  onClick={() => setSteering('action')}
                >
                  <ListPlus />
                  指派动作
                </Button>
              </div>
              {task.status !== 'active' && (
                <p className="mt-1.5 text-2xs text-text-tertiary">任务已结束，可在对话中续接</p>
              )}
            </section>

          </div>
        ) : null}
      </div>

      {task && steering === 'goal' && (
        <AdjustGoalDialog task={task} open onOpenChange={(o) => !o && setSteering(null)} />
      )}
      {task && steering === 'action' && (
        <AssignActionDialog task={task} open onOpenChange={(o) => !o && setSteering(null)} />
      )}

      {/* 停止确认：与对话页头部/表格操作列同一文案与语义。 */}
      <ConfirmDialog
        open={confirmTerminate}
        onOpenChange={setConfirmTerminate}
        title="停止任务"
        danger
        confirmLabel="确认停止"
        message={
          task
            ? `任务「${goalHead(task.goal)}」将落终态，进行中的 agent 立即退出（可重新下发）。`
            : ''
        }
        onConfirm={() => void doControl('terminate')}
      />
    </div>
  )
}
