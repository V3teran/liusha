import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Crosshair, Pause, Play, Square } from 'lucide-react'
import { listMessages } from '@/api/conversations'
import { controlTask } from '@/api/tasks'
import { ApiError } from '@/api/client'
import { taskKeys, useTaskDetail, useTaskUsage } from '@/features/tasks/useTaskQueries'
import { conversationKeys, useConversation } from './useConversationQueries'
import { useConversationStore } from '@/stores/conversation'
import { openEventStream, type StreamHandle, type StreamStatus } from '@/hooks/useEventStream'
import { fullTime, humanDuration, humanTokens } from '@/lib/format'
import { TokenPair } from '@/components/TokenPair'
import { taskStatusMeta } from '@/lib/taskStatus'
import { cn } from '@/lib/utils'
import { Composer } from './Composer'
import { TimelineThread } from './TimelineThread'
import { Button } from '@/components/ui/Button'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { EmptyState } from '@/components/ui/EmptyState'
import { ErrorState } from '@/components/ui/ErrorState'
import { toast } from '@/components/ui/toast'

interface ConversationDetailProps {
  convId?: string
  source: 'direct' | 'proxy'
  onStarted: (convID: string) => void
  onRunningChanged: () => void
  /** 执行轨迹按哪个智能体筛选（页面 URL 上的 agent）；null = 全部 */
  agent: string | null
  onAgentChange: (name: string | null) => void
  /** 放在顶栏最前面的控件（宽屏下页面放「收起 / 展开对话列表」的开关）；没有对话时由页面自己放 */
  leading?: ReactNode
}


// 会话详情主区（主从双栏的「从」）：给定 convId，渲染其作战轨迹 + 顶部状态栏 + 插话框。
// 人工下发(direct) 与 代理捕获(proxy) 两页共用此组件——统一骨架，右侧渲染同一 TimelineThread
// （多代理→脊柱缩进分叉；单代理→自然扁平），差异由数据本身表达，不做两套渲染器。
//
// 对话只是通道：消息与时间线属于对话；状态、暂停、用量与控制都属于对话所属的任务，
// 按任务 ID 取（纯聊天没有任务，不显示这些）。
//
// 去处（任务、发现、探索图、模型日志）都在对话列表行的「⋯」菜单里，顶栏不放。
export function ConversationDetail({ convId, source, onStarted, onRunningChanged, agent, onAgentChange, leading }: ConversationDetailProps) {
  const store = useConversationStore()
  const [loading, setLoading] = useState(false)
  // 加载失败分两态：missing = 对话不存在（已删除的旧深链，重试无意义）；failed = 可重试的故障。
  const [loadError, setLoadError] = useState<'missing' | 'failed' | null>(null)
  const [streamStatus, setStreamStatus] = useState<StreamStatus>('open')
  const handleRef = useRef<StreamHandle | null>(null)
  const refreshDebounceRef = useRef<number | undefined>(undefined)
  const queryClient = useQueryClient()
  // 会话代际：每次打开会话与卸载都 +1。异步请求返回时代际已变 = 用户已切走或页面已离开，
  // 结果必须丢弃——否则迟到的历史会在卸载后重建 SSE、迟到的补拉会混进新会话。
  // （只比 convId 不够：卸载不改变 convId，同一会话重进也不改变。）
  const generationRef = useRef(0)

  const hasConv = !!convId
  const startedAt = store.messages[0]?.created_at ?? ''
  // 所属任务（纯聊天为空；聊天转为执行时中途关联上，见下方的事件驱动刷新）
  const convQuery = useConversation(convId ?? '')
  const taskID = convQuery.data?.task_id || ''
  const task = useTaskDetail(taskID || null).data
  // 场景控件：新对话、或已加载且尚未绑定任务的对话由用户选；绑定了任务则只读展示
  // 任务建时快照的场景（进行中与已完成的任务都不能改场景，但控件不消失）。
  const scenarioControl = !convId || (!!convQuery.data && !taskID)
    ? { mode: 'pick' as const }
    : {
        mode: 'locked' as const,
        label:
          task === undefined
            ? '加载中…'
            : task.scenario
              ? task.scenario_deleted
                ? `${task.scenario_name}（已删除）`
                : task.scenario_name
              : '无场景',
      }
  // 任务三档运行态：active（含暂停子态）/ 终态 / 无任务。控制组与状态文案据此派生。
  const taskActive = hasConv && !!taskID && task?.status === 'active'
  const paused = taskActive && !!task?.paused
  const scanning = taskActive && !paused
  const topStatus = taskStatusMeta(taskID ? task?.status : undefined)
  const usage = useTaskUsage(taskID || null, taskActive).data

  // 事件驱动刷新：每有新消息落定（seq 增长）防抖 800ms 重取对话（可能刚关联上任务）与任务的
  // 状态、用量；任务进行中另有轮询兜底「最后一条消息之后的状态翻转」。
  const refreshKeysRef = useRef({ convId, taskID })
  refreshKeysRef.current = { convId, taskID }
  const scheduleRefresh = useCallback(() => {
    if (refreshDebounceRef.current) clearTimeout(refreshDebounceRef.current)
    refreshDebounceRef.current = window.setTimeout(() => {
      const { convId: c, taskID: t } = refreshKeysRef.current
      if (c) void queryClient.invalidateQueries({ queryKey: conversationKeys.detail(c) })
      if (t) {
        void queryClient.invalidateQueries({ queryKey: taskKeys.detail(t) })
        void queryClient.invalidateQueries({ queryKey: taskKeys.usage(t) })
      }
    }, 800)
  }, [queryClient])

  const resetToEmpty = useCallback(() => {
    handleRef.current?.close()
    handleRef.current = null
    store.reset()
    setStreamStatus('open')
    setLoading(false)
    setLoadError(null)
  }, [store])

  const open = useCallback(
    async (convID: string) => {
      const gen = ++generationRef.current
      handleRef.current?.close()
      store.reset()
      setLoadError(null)
      setLoading(true)
      setStreamStatus('open')
      let history
      try {
        history = await listMessages(convID)
      } catch (e) {
        if (generationRef.current === gen) {
          setLoading(false)
          setLoadError(e instanceof ApiError && e.status === 404 ? 'missing' : 'failed')
        }
        return
      }
      if (generationRef.current !== gen) return // 期间切了会话或已卸载→丢弃这批历史，不建连接
      setLoading(false)
      for (const m of history) store.ingest(m)
      handleRef.current = openEventStream(convID, store, setStreamStatus)
    },
    [store],
  )

  // convId 变化即切会话（含切到 undefined=新建态）。
  useEffect(() => {
    if (convId) void open(convId)
    else resetToEmpty()
    return () => {
      generationRef.current++
      handleRef.current?.close()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [convId])

  // 每有新消息落定（seq 增长）→防抖刷新。
  useEffect(() => {
    if (store.lastSeq > 0) scheduleRefresh()
  }, [store.lastSeq, scheduleRefresh])

  // 运行态翻转→通知父刷新左列表状态点（无需手动点 ↻）。
  const prevScanningRef = useRef(scanning)
  useEffect(() => {
    if (prevScanningRef.current !== scanning) onRunningChanged()
    prevScanningRef.current = scanning
  }, [scanning, onRunningChanged])

  useEffect(
    () => () => {
      if (refreshDebounceRef.current) clearTimeout(refreshDebounceRef.current)
    },
    [],
  )

  const tokenTotal = usage ? usage.tokens.in + usage.tokens.out : 0
  const tokenTip = usage
    ? [
        `输入 ${humanTokens(usage.tokens.in)}`,
        `输出 ${humanTokens(usage.tokens.out)}`,
        `其中缓存命中 ${humanTokens(usage.tokens.cached)}`,
        `合计 ${humanTokens(tokenTotal)}`,
        `${usage.llm_calls} 次 LLM 调用`,
      ].join(' · ')
    : ''
  const durationTip = usage
    ? [
        `墙钟 ${humanDuration(usage.elapsed_ms)}（发起→完成）`,
        `工作时间 ${humanDuration(usage.llm_latency_ms + usage.tool_duration_ms)}（含子代理并发累加，故 > 墙钟）`,
        `LLM ${humanDuration(usage.llm_latency_ms)} · 工具 ${humanDuration(usage.tool_duration_ms)} · ${usage.tool_calls} 次工具调用`,
      ].join(' · ')
    : ''

  const handleStarted = (convID: string) => onStarted(convID)
  // 多轮追加（如"继续"）：api 落的 user 消息不经 SSE，主动拉增量补进 store（游标=发送前 seq 快照）。
  const handleAppended = async (afterSeq: number) => {
    if (!convId) return
    const gen = generationRef.current
    const msgs = await listMessages(convId, afterSeq)
    if (generationRef.current !== gen) return // 补拉期间已切走：结果属于旧会话，丢弃
    for (const m of msgs) store.ingest(m)
  }
  const retryLoad = () => {
    if (convId) void open(convId)
  }
  // 任务控制（暂停/继续/停止）：与任务页操作列同一端点、同一语义。会话是
  // 实时观战的驾驶舱，控制组与任务页 parity——只有停止没有暂停的驾驶舱是
  // 不完整的控制面。失败就地告警（全站无 toast 的就近反馈惯例）。
  // 暂停/继续是同一可逆状态转换的两个方向，合并为随状态翻转的单切换钮；
  // 只有不可逆的停止过确认弹层（原生 confirm 无法套 Token）。
  const [confirmTerminate, setConfirmTerminate] = useState(false)
  const doControl = async (action: 'pause' | 'resume' | 'terminate') => {
    if (!taskID) return
    try {
      await controlTask(taskID, action)
    } catch (e) {
      toast.error(`操作失败：${(e as Error).message}`)
      return
    }
    await queryClient.invalidateQueries({ queryKey: taskKeys.detail(taskID) })
  }
  const control = async (action: 'pause' | 'resume' | 'terminate') => {
    if (action === 'terminate') {
      setConfirmTerminate(true)
      return
    }
    await doControl(action)
  }


  return (
    <section className="flex h-full min-h-0 min-w-0 flex-col">
      {hasConv && (
        <div className="flex flex-shrink-0 flex-wrap items-center justify-between gap-x-4 gap-y-1.5 whitespace-nowrap border-b border-border px-4 py-2.5">
          <div className="inline-flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            {leading}
            <span className="inline-flex items-center gap-2 text-sm text-text-secondary">
              <span
                className={cn('h-2 w-2 rounded-full', scanning && 'motion-safe:animate-pulse')}
                style={{ background: topStatus.color }}
              />
              {paused ? '已暂停（进度保留）' : scanning ? '智能体工作中…' : topStatus.label}
            </span>
            {streamStatus === 'reconnecting' && (
              <span className="inline-flex items-center gap-1.5 text-xs text-status-warning" title="实时连接断开，正在自动重连…">
                <span className="h-[11px] w-[11px] animate-spin rounded-full border-2 border-status-warning/40 border-t-status-warning" />
                重连中…
              </span>
            )}
          </div>
          <div className="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
            {startedAt && (
              <span className="font-mono text-xs text-text-secondary" title={`发起于 ${fullTime(startedAt)}`}>
                发起 {fullTime(startedAt)}
              </span>
            )}
            <span className="inline-flex items-center gap-2.5">
              {usage && tokenTotal > 0 && (
                <span className="inline-flex flex-wrap items-center gap-x-3.5 gap-y-1">
                  <span className="inline-flex items-baseline gap-1.5 text-xs" title={tokenTip}>
                    <span className="text-text-secondary">tokens</span>
                    <span className="font-mono text-text">
                      <TokenPair inTokens={usage.tokens.in} outTokens={usage.tokens.out} cachedTokens={usage.tokens.cached} />
                    </span>
                  </span>
                  <span className="inline-flex items-baseline gap-1.5 text-xs" title={durationTip}>
                    <span className="text-text-secondary">耗时</span>
                    <span className="font-mono text-text">{humanDuration(usage.elapsed_ms)}</span>
                  </span>
                </span>
              )}
              {/* 控制组与任务表格同一门控：任务 active 才出现（终态行不渲染，
                  状态点已表达终态）；纯聊天（无任务对象）同样不渲染。
                  暂停/继续合并为单切换钮（同一可逆转换的两个方向），停止独立。 */}
              {taskActive && (
                <>
                  <Button
                    variant="secondary"
                    size="xs"
                    className="w-7 px-0"
                    onClick={() => void control(paused ? 'resume' : 'pause')}
                    aria-label={paused ? '继续任务' : '暂停任务'}
                    title={paused ? '继续（从探索图进度恢复执行）' : '暂停（保留进度，可随时继续）'}
                  >
                    {paused ? <Play /> : <Pause />}
                  </Button>
                  <Button
                    variant="danger"
                    size="xs"
                    className="w-7 px-0"
                    onClick={() => void control('terminate')}
                    aria-label="停止任务"
                    title="停止（终态，可重新下发）"
                  >
                    <Square className="fill-current" />
                  </Button>
                </>
              )}
            </span>
          </div>
        </div>
      )}

      {loadError ? (
        loadError === 'missing' ? (
          <ErrorState title="对话不存在" message="它可能已被删除，从左侧重新选择一个对话。" className="min-h-0 flex-1" />
        ) : (
          <ErrorState
            title="加载对话失败"
            message="无法拉取历史消息，可能是网络或服务暂时不可用。"
            onRetry={retryLoad}
            className="min-h-0 flex-1"
          />
        )
      ) : loading ? (
        <div className="flex flex-1 min-h-0 flex-col gap-4 overflow-hidden px-4 py-5" aria-busy="true" aria-label="正在加载对话">
          {[1, 2, 3, 4, 5].map((n) => (
            <div key={n} className={cn('flex max-w-[60%] flex-col gap-1.5', n % 2 === 0 && 'self-end items-end')}>
              <div className="h-[11px] w-[180px] animate-pulse rounded-md bg-surface-2" />
              <div className="h-[11px] w-[260px] animate-pulse rounded-md bg-surface-2" />
              <div className="h-[11px] w-[120px] animate-pulse rounded-md bg-surface-2" />
            </div>
          ))}
        </div>
      ) : hasConv || store.messages.length > 0 ? (
        <TimelineThread active={scanning} elapsedMs={usage?.elapsed_ms} agent={agent} onAgentChange={onAgentChange} />
      ) : source === 'proxy' ? (
        /* 代理捕获没有发起入口：未选中批次就是普通的「未选择」空态 */
        <EmptyState
          kind="unselected"
          noun="代理批次"
          hint="选择一个批次查看分析过程。"
          className="min-h-0 flex-1"
        />
      ) : (
        /* 人工下发的新对话：发起引导（对话框就在下方），不是空态 */
        <div className="flex flex-1 min-h-0 items-center justify-center p-6">
          <div className="flex max-w-[420px] flex-col items-center text-center text-text-secondary">
            <span className="flex h-11 w-11 items-center justify-center rounded-full bg-accent-soft text-accent">
              <Crosshair aria-hidden className="h-5 w-5" />
            </span>
            <h2 className="mb-1.5 mt-3 text-base font-semibold text-text">发起一次渗透测试任务</h2>
            <p className="text-sm leading-relaxed">
              在下方描述目标（URL / 账号 / 测试方向），实时观察智能体调度、工具调用与发现产出。
            </p>
          </div>
        </div>
      )}

      {/* 对话框恒渲染：聊天界面框架不随数据缺失消失。代理捕获未选批次时
          禁用（无可附着会话），placeholder 引导先选批次。 */}
      <Composer
        convId={convId}
        disabled={source === 'proxy' && !hasConv}
        placeholder={
          source === 'proxy' && !hasConv ? '选择代理批次后，可在这里插话追问…' : undefined
        }
        scenarioControl={scenarioControl}
        onStarted={handleStarted}
        onAppended={handleAppended}
      />

      {/* 停止确认：不可逆操作走 danger 形态；暂停/继续可逆不设确认。 */}
      <ConfirmDialog
        open={confirmTerminate}
        onOpenChange={setConfirmTerminate}
        title="停止任务"
        danger
        confirmLabel="确认停止"
        message="任务将落终态，进行中的 agent 立即退出（可重新下发）。"
        onConfirm={() => void doControl('terminate')}
      />
    </section>
  )
}
