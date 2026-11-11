import { useCallback, useEffect, useRef, useState } from 'react'
import { Pause, Play, Square } from 'lucide-react'
import { getConversationUsage, listMessages } from '@/api/conversations'
import { controlTask } from '@/api/tasks'
import type { ConversationUsage } from '@/api/types'
import { ApiError } from '@/api/client'
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

interface ConversationDetailProps {
  convId?: string
  source: 'direct' | 'proxy'
  onStarted: (convID: string) => void
  onRunningChanged: () => void
}

// 空状态文案按来源区分（direct 可发起、proxy 等流量）。
function emptyCopy(source: 'direct' | 'proxy') {
  return source === 'proxy'
    ? {
        mark: '⇄',
        title: '选择一批流量查看分析',
        desc: '左侧是代理捕获的流量批次，AI 已逐批分析。点开任意一批，实时观察分析轨迹与漏洞产出，可随时插话追问。',
      }
    : {
        mark: '⌖',
        title: '发起一次渗透测试任务',
        desc: '在下方描述目标（URL / 账号 / 测试方向），实时观察智能体调度、工具调用与漏洞产出。',
      }
}

// 会话详情主区（主从双栏的「从」）：给定 convId，渲染其作战轨迹 + 顶部状态栏 + 插话框。
// 人工下发(direct) 与 代理捕获(proxy) 两页共用此组件——统一骨架，右侧渲染同一 TimelineThread
// （多代理→脊柱缩进分叉；单代理→自然扁平），差异由数据本身表达，不做两套渲染器。
export function ConversationDetail({ convId, source, onStarted, onRunningChanged }: ConversationDetailProps) {
  const store = useConversationStore()
  const [loading, setLoading] = useState(false)
  // 加载失败分两态：missing = 会话不存在（已删除的旧深链，重试无意义）；failed = 可重试的故障。
  const [loadError, setLoadError] = useState<'missing' | 'failed' | null>(null)
  const [streamStatus, setStreamStatus] = useState<StreamStatus>('open')
  const [usage, setUsage] = useState<ConversationUsage | null>(null)
  const handleRef = useRef<StreamHandle | null>(null)
  const usageDebounceRef = useRef<number | undefined>(undefined)
  const convIdRef = useRef(convId)
  convIdRef.current = convId
  // 会话代际：每次打开会话与卸载都 +1。异步请求返回时代际已变 = 用户已切走或页面已离开，
  // 结果必须丢弃——否则迟到的历史会在卸载后重建 SSE、迟到的补拉会混进新会话。
  // （只比 convId 不够：卸载不改变 convId，同一会话重进也不改变。）
  const generationRef = useRef(0)

  const hasConv = !!convId
  const startedAt = store.messages[0]?.created_at ?? ''
  // 任务三档运行态：active（含暂停子态）/ 终态 / 无任务。控制组与状态文案据此派生。
  const taskID = usage?.task_id || ''
  const taskActive = hasConv && usage?.status === 'active' && !!taskID
  const paused = taskActive && (usage?.paused ?? false)
  const scanning = hasConv && (usage?.running ?? false) && !paused
  const topStatus = taskStatusMeta(usage?.status)

  const refreshUsage = useCallback(async () => {
    const reqConv = convIdRef.current
    if (!reqConv) return
    try {
      const u = await getConversationUsage(reqConv)
      if (convIdRef.current === reqConv) {
        setUsage(u)
      }

    } catch {
      // 静默
    }
  }, [])

  // 事件驱动刷新：每有新消息落定（seq 增长）防抖 800ms 刷用量，不依赖固定轮询启发。
  const scheduleUsageRefresh = useCallback(() => {
    if (usageDebounceRef.current) clearTimeout(usageDebounceRef.current)
    usageDebounceRef.current = window.setTimeout(refreshUsage, 800)
  }, [refreshUsage])

  const resetToEmpty = useCallback(() => {
    handleRef.current?.close()
    handleRef.current = null
    store.reset()
    setUsage(null)
    setStreamStatus('open')
    setLoading(false)
    setLoadError(null)
  }, [store])

  const open = useCallback(
    async (convID: string) => {
      const gen = ++generationRef.current
      handleRef.current?.close()
      store.reset()
      setUsage(null)
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
      void refreshUsage()
    },
    [store, refreshUsage],
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

  // 每有新消息落定（seq 增长）→防抖刷用量。
  useEffect(() => {
    scheduleUsageRefresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [store.lastSeq])

  // 运行中兜底轮询：任务仍在 active（含暂停——等待继续翻转）每 4s 拉权威用量，
  // 补「最后事件后终态翻转」；终态 running=false 自停。
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (taskActive) void refreshUsage()
    }, 4000)
    return () => clearInterval(timer)
  }, [taskActive, refreshUsage])

  // 运行态翻转→通知父刷新左列表状态点（无需手动点 ↻）。
  const prevScanningRef = useRef(scanning)
  useEffect(() => {
    if (prevScanningRef.current !== scanning) onRunningChanged()
    prevScanningRef.current = scanning
  }, [scanning, onRunningChanged])

  useEffect(
    () => () => {
      if (usageDebounceRef.current) clearTimeout(usageDebounceRef.current)
    },
    [],
  )

  const tokenTip = usage
    ? [
        `输入 ${humanTokens(usage.tokens.in)}`,
        `输出 ${humanTokens(usage.tokens.out)}`,
        `其中缓存命中 ${humanTokens(usage.tokens.cached)}`,
        `合计 ${humanTokens(usage.tokens.total)}`,
        `${usage.llm_calls} 次 LLM 调用`,
      ].join(' · ')
    : ''
  const durationTip = usage
    ? [
        `墙钟 ${humanDuration(usage.duration_ms)}（发起→完成）`,
        `工作时间 ${humanDuration(usage.work_ms)}（含子代理并发累加，故 > 墙钟）`,
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
      window.alert(`操作失败：${(e as Error).message}`)
      return
    }
    await refreshUsage()
  }
  const control = async (action: 'pause' | 'resume' | 'terminate') => {
    if (action === 'terminate') {
      setConfirmTerminate(true)
      return
    }
    await doControl(action)
  }

  const copy = emptyCopy(source)

  return (
    <section className="flex h-full min-h-0 min-w-0 flex-col">
      {hasConv && (
        <div className="flex flex-shrink-0 flex-wrap items-center justify-between gap-x-4 gap-y-1.5 whitespace-nowrap border-b border-border px-4 py-2.5">
          <div className="inline-flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <span className="inline-flex items-center gap-2 text-sm text-text-secondary">
              <span
                className={cn('h-2 w-2 rounded-full', scanning && 'animate-pulse')}
                style={{ background: topStatus.color }}
              />
              {paused ? '已暂停（进度保留）' : scanning ? '智能体工作中…' : topStatus.label}
            </span>
            {streamStatus === 'reconnecting' && (
              <span className="inline-flex items-center gap-1.5 text-xs text-sev-medium" title="实时连接断开，正在自动重连…">
                <span className="h-[11px] w-[11px] animate-spin rounded-full border-2 border-sev-medium/40 border-t-sev-medium" />
                重连中…
              </span>
            )}
            {startedAt && (
              <span className="font-mono text-xs text-text-secondary" title={`发起于 ${fullTime(startedAt)}`}>
                发起 {fullTime(startedAt)}
              </span>
            )}
          </div>
          <div className="inline-flex items-center gap-3">
            <span className="inline-flex items-center gap-2.5">
              {usage && usage.tokens.total > 0 && (
                <span className="inline-flex flex-wrap items-center gap-x-3.5 gap-y-1">
                  <span className="inline-flex items-baseline gap-1.5 text-xs" title={tokenTip}>
                    <span className="text-text-secondary">tokens</span>
                    <span className="font-mono text-text">
                      <TokenPair inTokens={usage.tokens.in} outTokens={usage.tokens.out} cachedTokens={usage.tokens.cached} />
                    </span>
                  </span>
                  <span className="inline-flex items-baseline gap-1.5 text-xs" title={durationTip}>
                    <span className="text-text-secondary">耗时</span>
                    <span className="font-mono text-text">{humanDuration(usage.duration_ms)}</span>
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
        <div className="flex flex-1 min-h-0 items-center justify-center p-6">
          <div className="max-w-[420px] text-center text-text-secondary">
            <div className="mb-3.5 text-4xl leading-none text-accent">⚠</div>
            {loadError === 'missing' ? (
              <>
                <h2 className="mb-2 text-[17px] text-text">会话不存在</h2>
                <p className="text-sm leading-relaxed">该会话可能已被删除，请从左侧列表重新选择。</p>
              </>
            ) : (
              <>
                <h2 className="mb-2 text-[17px] text-text">加载对话失败</h2>
                <p className="text-sm leading-relaxed">无法拉取历史消息，可能是网络或服务暂时不可用。</p>
                <Button className="mt-4" onClick={retryLoad}>
                  重试
                </Button>
              </>
            )}
          </div>
        </div>
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
        <TimelineThread active={scanning} elapsedMs={usage?.duration_ms} />
      ) : (
        <div className="flex flex-1 min-h-0 items-center justify-center p-6">
          <div className="max-w-[420px] text-center text-text-secondary">
            <div className="mb-3.5 text-4xl leading-none text-accent">{copy.mark}</div>
            <h2 className="mb-2 text-[17px] text-text">{copy.title}</h2>
            <p className="text-sm leading-relaxed">{copy.desc}</p>
          </div>
        </div>
      )}

      {/* 对话框恒渲染：聊天界面框架不随数据缺失消失。代理捕获未选批次时
          禁用（无可附着会话），placeholder 引导先选批次。 */}
      <Composer
        convId={convId}
        disabled={source === 'proxy' && !hasConv}
        placeholder={
          source === 'proxy' && !hasConv ? '选择左侧一批流量后，可在这里插话追问…' : undefined
        }
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
