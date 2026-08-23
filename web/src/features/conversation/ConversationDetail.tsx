import { useCallback, useEffect, useRef, useState } from 'react'
import { Square } from 'lucide-react'
import { abortScan, getConversationUsage, listMessages } from '@/api/client'
import type { ConversationUsage } from '@/api/types'
import { useConversationStore } from '@/stores/conversation'
import { openEventStream, type StreamHandle, type StreamStatus } from '@/hooks/useEventStream'
import { compactNumber, fullTime, humanDuration, humanTokens } from '@/lib/format'
import { scanStatusMeta } from '@/lib/scanStatus'
import { cn } from '@/lib/utils'
import { Composer } from './Composer'
import { TimelineThread } from './TimelineThread'

interface ConversationDetailProps {
  convId?: string
  source: 'manual' | 'auto'
  onStarted: (convID: string) => void
  onRunningChanged: () => void
}

// 空状态文案按来源区分（manual 可发起、auto 等流量）。
function emptyCopy(source: 'manual' | 'auto') {
  return source === 'auto'
    ? {
        mark: '⇄',
        title: '选择一批流量查看分析',
        desc: '左侧是代理捕获的流量批次，AI 已逐批分析。点开任意一批，实时观察分析轨迹与漏洞产出，可随时插话追问。',
      }
    : {
        mark: '⌖',
        title: '发起一次渗透扫描',
        desc: '在下方选择场景、描述目标（URL / 账号 / 测试方向），实时观察智能体调度、工具调用与漏洞产出。',
      }
}

// 会话详情主区（主从双栏的「从」）：给定 convId，渲染其作战轨迹 + 顶部状态栏 + 插话框。
// 主动下发(manual) 与 被动代理(auto) 两页共用此组件——统一骨架，右侧渲染同一 TimelineThread
// （多代理→脊柱缩进分叉；单代理→自然扁平），差异由数据本身表达，不做两套渲染器。
export function ConversationDetail({ convId, source, onStarted, onRunningChanged }: ConversationDetailProps) {
  const store = useConversationStore()
  const [loading, setLoading] = useState(false) // 补历史中→骨架屏
  const [loadError, setLoadError] = useState(false) // 补历史失败→错误重试卡
  const [streamStatus, setStreamStatus] = useState<StreamStatus>('open')
  const [usage, setUsage] = useState<ConversationUsage | null>(null)
  const handleRef = useRef<StreamHandle | null>(null)
  const usageDebounceRef = useRef<number | undefined>(undefined)
  const convIdRef = useRef(convId)
  convIdRef.current = convId

  const hasConv = !!convId
  const startedAt = store.messages[0]?.CreatedAt ?? ''
  // 权威运行态：后端 usage.running（active_scan/passive_session 是否仍 active）。
  const scanning = hasConv && (usage?.running ?? false)
  // 顶部三态（进行中/已完成/已中止）用 usage.status 真实态，与左列表共用 scanStatusMeta。
  const topStatus = scanStatusMeta(usage?.status)

  const refreshUsage = useCallback(async () => {
    const reqConv = convIdRef.current
    if (!reqConv) return
    try {
      const u = await getConversationUsage(reqConv)
      // stale 防护：在途期间用户已切走会话→丢弃旧响应，避免把上个会话的用量短暂写到当前头部。
      if (convIdRef.current === reqConv) setUsage(u)
    } catch {
      // 静默：用量是增强信息，拉取失败不打断观察。
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
    setLoadError(false)
  }, [store])

  const open = useCallback(
    async (convID: string) => {
      handleRef.current?.close()
      store.reset()
      setUsage(null)
      setLoadError(false)
      setLoading(true)
      setStreamStatus('open')
      let history
      try {
        history = await listMessages(convID)
      } catch {
        if (convIdRef.current === convID) {
          setLoading(false)
          setLoadError(true)
        }
        return
      }
      if (convIdRef.current !== convID) return // 期间又切了会话→丢弃这批历史
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
      handleRef.current?.close()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [convId])

  // 每有新消息落定（seq 增长）→防抖刷用量。
  useEffect(() => {
    scheduleUsageRefresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [store.lastSeq])

  // 运行中兜底轮询：进行时每 4s 拉权威用量，补「最后事件后终态翻转」；终态 running=false 自停。
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (scanning) void refreshUsage()
    }, 4000)
    return () => clearInterval(timer)
  }, [scanning, refreshUsage])

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
    for (const m of await listMessages(convId, afterSeq)) store.ingest(m)
  }
  const retryLoad = () => {
    if (convId) void open(convId)
  }
  const stop = async () => {
    if (convId) await abortScan(convId)
  }

  const copy = emptyCopy(source)

  return (
    <section className="flex h-full min-h-0 min-w-0 flex-col">
      {hasConv && (
        <div className="flex flex-shrink-0 items-center justify-between border-b border-border px-4 py-2.5">
          <div className="inline-flex items-center gap-3">
            <span className="inline-flex items-center gap-2 text-[13px] text-muted">
              <span
                className={cn('h-2 w-2 rounded-full', scanning && 'animate-pulse')}
                style={{ background: topStatus.color }}
              />
              {scanning ? 'agent 工作中…' : topStatus.label}
            </span>
            {streamStatus === 'reconnecting' && (
              <span className="inline-flex items-center gap-1.5 text-xs text-sev-medium" title="实时连接断开，正在自动重连…">
                <span className="h-[11px] w-[11px] animate-spin rounded-full border-2 border-sev-medium/40 border-t-sev-medium" />
                重连中…
              </span>
            )}
            {startedAt && (
              <span className="font-mono text-[11.5px] text-muted" title={`发起于 ${fullTime(startedAt)}`}>
                发起 {fullTime(startedAt)}
              </span>
            )}
          </div>
          <div className="inline-flex items-center gap-3">
            {usage && usage.tokens.total > 0 && (
              <span className="inline-flex items-center gap-3.5">
                <span className="inline-flex items-baseline gap-1.5 text-xs" title={tokenTip}>
                  <span className="text-muted">tokens</span>
                  <span className="font-mono text-text">
                    <span className="text-sev-info" title="输入">
                      ↑{compactNumber(usage.tokens.in)}
                    </span>{' '}
                    <span className="text-sev-high" title="输出">
                      ↓{compactNumber(usage.tokens.out)}
                    </span>
                  </span>
                </span>
                <span className="inline-flex items-baseline gap-1.5 text-xs" title={durationTip}>
                  <span className="text-muted">耗时</span>
                  <span className="font-mono text-text">{humanDuration(usage.duration_ms)}</span>
                </span>
              </span>
            )}
            {scanning && (
              <button
                type="button"
                onClick={() => void stop()}
                className="inline-flex items-center gap-1.5 rounded-lg border border-red-500 bg-red-500/10 px-3 py-1 text-[12.5px] font-medium text-red-500 hover:bg-red-500/20"
              >
                <Square className="h-3 w-3 fill-current" />
                停止扫描
              </button>
            )}
          </div>
        </div>
      )}

      {loadError ? (
        <div className="flex flex-1 min-h-0 items-center justify-center p-6">
          <div className="max-w-[420px] text-center text-muted">
            <div className="mb-3.5 text-4xl leading-none text-accent">⚠</div>
            <h2 className="mb-2 text-[17px] text-text">加载对话失败</h2>
            <p className="text-[13px] leading-relaxed">无法拉取历史消息，可能是网络或服务暂时不可用。</p>
            <button
              type="button"
              onClick={retryLoad}
              className="mt-4 rounded-lg bg-accent px-5 py-2 text-[13px] text-white hover:bg-accent-hover"
            >
              重试
            </button>
          </div>
        </div>
      ) : loading ? (
        <div className="flex flex-1 min-h-0 flex-col gap-4.5 overflow-hidden px-4 py-5" aria-busy="true" aria-label="正在加载对话">
          {[1, 2, 3, 4, 5].map((n) => (
            <div key={n} className={cn('flex max-w-[60%] flex-col gap-1.5', n % 2 === 0 && 'self-end items-end')}>
              <div className="h-[11px] w-[180px] animate-pulse rounded-md bg-surface-2" />
              <div className="h-[11px] w-[260px] animate-pulse rounded-md bg-surface-2" />
              <div className="h-[11px] w-[120px] animate-pulse rounded-md bg-surface-2" />
            </div>
          ))}
        </div>
      ) : hasConv || store.messages.length > 0 ? (
        <TimelineThread />
      ) : (
        <div className="flex flex-1 min-h-0 items-center justify-center p-6">
          <div className="max-w-[420px] text-center text-muted">
            <div className="mb-3.5 text-4xl leading-none text-accent">{copy.mark}</div>
            <h2 className="mb-2 text-[17px] text-text">{copy.title}</h2>
            <p className="text-[13px] leading-relaxed">{copy.desc}</p>
          </div>
        </div>
      )}

      {/* 被动代理(auto) 无「发起新扫描」：仅当有选中会话时才露插话框；主动下发(manual) 允许空态发起。 */}
      {(source === 'manual' || hasConv) && (
        <Composer
          convId={convId}
          scanning={scanning}
          onStarted={handleStarted}
          onAppended={handleAppended}
          onStop={() => void stop()}
        />
      )}
    </section>
  )
}
