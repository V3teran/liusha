import { useCallback, useEffect, useRef, useState, useMemo } from 'react'
import { Brain, ChevronDown, Loader2 } from 'lucide-react'
import { useConversationStore } from '@/stores/conversation'
import { dayKey, dayLabel } from '@/lib/format'
import { buildThreadRows, inFlightToolCalls, pairToolResults, type ThreadRow } from '@/lib/threadRows'
import { classifyMessage } from '@/lib/messageKind'
import { useTypewriter } from '@/hooks/useTypewriter'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { AgentCard } from './AgentCard'
import { StepTools } from './StepTools'
import { CommandBlock } from './cards/CommandBlock'
import { ReplyBlock } from './cards/ReplyBlock'
import { ReasoningCard } from './cards/ReasoningCard'
import { ToolCallCard } from './cards/ToolCallCard'
import { ToolResultCard } from './cards/ToolResultCard'
import { FindingCard } from './cards/FindingCard'
import { TaskStatusCard } from './cards/TaskStatusCard'

function rowAgent(r: ThreadRow): string {
  if (r.kind === 'tools') return r.tools[0]?.metadata?.agent_name || ''
  if (r.kind === 'msg') return r.msg.metadata?.agent_name || ''
  return ''
}

function cardCopyText(r: Exclude<ThreadRow, { kind: 'divider' | 'tools' }>): string | undefined {
  const m = r.msg
  if (classifyMessage(m) === 'reasoning') return m.metadata?.text || m.content
  return undefined
}

function RunningIndicator({ elapsedMs }: { elapsedMs?: number }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(t)
  }, [])
  const baseRef = useRef<{ ms: number; at: number }>({ ms: elapsedMs ?? 0, at: Date.now() })
  useEffect(() => {
    const prev = baseRef.current
    if ((elapsedMs ?? 0) >= prev.ms) baseRef.current = { ms: elapsedMs ?? 0, at: Date.now() }
  }, [elapsedMs])
  const shown = Math.max(0, baseRef.current.ms + (now - baseRef.current.at))
  const mm = Math.floor(shown / 60000)
  const ss = Math.floor((shown % 60000) / 1000)
  const label = mm >= 60 ? `${Math.floor(mm / 60)}h${mm % 60}m` : `${mm}m${String(ss).padStart(2, '0')}s`
  return (
    <div className="flex justify-center py-2" role="status" aria-live="polite" data-running-indicator>
      <div className="inline-flex items-center gap-2 rounded-full border border-border bg-surface-2/60 px-4 py-1.5 text-xs text-muted">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        <span>agent 工作中</span>
        <span className="font-mono text-xs">{label}</span>
      </div>
    </div>
  )
}

export function TimelineThread({ active, elapsedMs }: { active?: boolean; elapsedMs?: number }) {
  const messages = useConversationStore((s) => s.messages)
  const liveStreams = useConversationStore((s) => s.liveStreams)
  const liveOrder = useConversationStore((s) => s.liveOrder)
  const thinking = useConversationStore((s) => s.thinking)
  const elRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)
  const [unread, setUnread] = useState(0)
  const prevLenRef = useRef(0)
  const { copiedKey, copy } = useCopyToClipboard()

  // 派生值 memo 化：本组件订阅 liveStreams，SSE 每帧 delta 都会重渲染——
  // 三个全量消息推导（行构建/在途工具/结果配对）不缓存的话每帧 O(N) 重算 + 下游卡片全量重渲染。
  const rows = useMemo(() => buildThreadRows(messages, { dayKey, dayLabel }), [messages, dayKey, dayLabel])
  const runningTools = useMemo(() => (active ? inFlightToolCalls(messages) : undefined), [active, messages])
  const toolPairs = useMemo(() => pairToolResults(messages), [messages])
  const activityCount = useMemo(() => rows.reduce((n, r) => (r.kind === 'divider' ? n : n + 1), 0), [rows])

  const nearBottom = useCallback(() => {
    const e = elRef.current
    if (!e) return true
    return e.scrollHeight - e.scrollTop - e.clientHeight < 120
  }, [])

  const stickToBottom = useCallback(() => {
    const stick = nearBottom()
    requestAnimationFrame(() => {
      if (stick && elRef.current) elRef.current.scrollTop = elRef.current.scrollHeight
    })
  }, [nearBottom])

  const onScroll = useCallback(() => {
    setAtBottom((wasBottom) => {
      const isBottom = nearBottom()
      if (isBottom && !wasBottom) setUnread(0)
      return isBottom
    })
  }, [nearBottom])

  const scrollToBottom = useCallback(() => {
    if (elRef.current) elRef.current.scrollTop = elRef.current.scrollHeight
    setUnread(0)
  }, [])

  useEffect(() => {
    const n = activityCount
    const prev = prevLenRef.current
    if (nearBottom()) {
      stickToBottom()
    } else if (n > prev) {
      setUnread((u) => u + (n - prev))
    }
    prevLenRef.current = n
  }, [activityCount, nearBottom, stickToBottom])

  useEffect(() => {
    stickToBottom()
  }, [liveOrder, liveStreams, active, stickToBottom])

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div
        ref={elRef}
        className="flex-1 overflow-y-auto py-4 pl-2 pr-4"
        role="log"
        aria-live="polite"
        aria-relevant="additions"
        aria-label="执行轨迹"
        onScroll={onScroll}
      >
        {rows.map((r) => {
          if (r.kind === 'divider') {
            return (
              <div key={r.key} className="ml-[26px] my-2.5 flex items-center gap-3 text-xs text-muted">
                <span className="h-px flex-1 bg-border" />
                <span className="flex-shrink-0 font-mono tracking-wide">{r.label}</span>
                <span className="h-px flex-1 bg-border" />
              </div>
            )
          }
          if (r.kind === 'msg' && classifyMessage(r.msg) === 'user') {
            return (
              <CommandBlock
                key={r.key}
                content={r.msg.content}
                createdAt={r.msg.created_at}
                copied={copiedKey === r.msg.id}
                onCopy={() => copy(r.msg.id, r.msg.content)}
              />
            )
          }
          if (r.kind === 'msg' && classifyMessage(r.msg) === 'assistant') {
            return (
              <div key={r.key} className="flex justify-start pb-5">
                <div className="flex w-full max-w-3xl flex-col items-start">
                  <ThinkingFold text={r.msg.metadata?.thinking} />
                  <ReplyBlock
                    content={r.msg.content}
                    agentName={r.msg.metadata?.agent_name}
                    createdAt={r.msg.created_at}
                    copied={copiedKey === r.msg.id}
                    onCopy={() => copy(r.msg.id, r.msg.content)}
                  />
                </div>
              </div>
            )
          }
          // 任务生命周期节点 → 居中系统窄条（不套 agent 卡——系统裁决无归属）。
          if (r.kind === 'msg' && classifyMessage(r.msg) === 'task-status') {
            return <TaskStatusCard key={r.key} md={r.msg.metadata!} />
          }
          if (r.kind === 'tools') {
            return (
              <StepTools
                key={r.key}
                tools={r.tools}
                runningSeqs={runningTools}
                pairs={toolPairs}
                ended={!active}
              />
            )
          }
          const agent = rowAgent(r)
          const copyText = cardCopyText(r)
          return (
            <AgentCard
              key={r.key}
              agentName={agent}
              createdAt={r.msg.created_at}
              copied={copiedKey === r.msg.id}
              onCopy={copyText ? () => copy(r.msg.id, copyText) : undefined}
              copyText={copyText}
            >
              {renderCard(r)}
            </AgentCard>
          )
        })}

        {thinking && (
          <div className="flex justify-start pb-5" role="status" aria-live="polite" data-thinking-node>
            <div className="flex w-full max-w-3xl flex-col items-start">
              <div className="flex items-center gap-2.5 rounded-2xl rounded-tl-md bg-surface-2 px-4 py-3">
                <span className="flex items-center gap-1">
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted [animation-delay:0ms]" />
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted [animation-delay:160ms]" />
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted [animation-delay:320ms]" />
                </span>
                <span className="text-sm text-muted">正在思考…</span>
              </div>
            </div>
          </div>
        )}

        {liveOrder.map((lane) => (
          <div key={lane} data-live-node aria-hidden="true">
            <LiveThoughtLane agentName={lane} lane={liveStreams[lane]} />
          </div>
        ))}

        {active && liveOrder.length === 0 && <RunningIndicator elapsedMs={elapsedMs} />}
      </div>

      {!atBottom && (
        <button
          type="button"
          onClick={scrollToBottom}
          className="absolute bottom-4 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1.5 rounded-full bg-accent-soft px-4 py-1.5 text-sm font-medium text-accent shadow-lg hover:bg-accent/10 hover:text-accent-hover"
        >
          <span>{unread > 0 ? `${unread > 99 ? '99+' : unread} 条新动态` : '回到最新'}</span>
          <span className="text-sm leading-none">↓</span>
        </button>
      )}
    </div>
  )
}

function renderCard(r: Exclude<ThreadRow, { kind: 'divider' | 'tools' }>) {
  const m = r.msg
  const md = m.metadata
  const tag = classifyMessage(m)

  switch (tag) {
    case 'reasoning': {
      const text = md?.text || m.content
      return (
        <ReasoningCard
          text={text}
          thinking={md?.thinking || undefined}
          agentName={md?.agent_name}
          step={r.step}
          inTokens={md?.in_tokens}
          outTokens={md?.out_tokens}
          latencyMs={md?.latency_ms}
        />
      )
    }
    case 'tool-call':
      return <ToolCallCard tool={md?.tool_name || ''} args={md?.args || ''} agentName={md?.agent_name} />
    case 'finding':
      return (
        <FindingCard
          summary={typeof md?.summary === 'string' ? md.summary : ''}
          severity={typeof md?.severity === 'string' ? md.severity : 'info'}
          host={typeof md?.host === 'string' ? md.host : undefined}
          target={typeof md?.target === 'string' ? md.target : undefined}
        />
      )
    case 'tool-result':
      return (
        <ToolResultCard
          tool={md?.tool_name || ''}
          result={md?.result || ''}
          durationMs={md?.duration_ms || 0}
          err={md?.err || ''}
          agentName={md?.agent_name}
        />
      )
    default:
      return null
  }
}

function ThinkingStream({ text }: { text: string | unknown }) {
  // useTypewriter 必须无条件调用（Rules of Hooks——text 由空变非空时 hook 数量
  // 不能变化），由 hasText 决定渲染与否。
  const hasText = !!text && typeof text === 'string' && text.trim() !== ''
  const typed = useTypewriter(hasText ? (text as string) : '')
  return (
    <div className="mb-2 rounded-lg border border-border/60 bg-background/40">
      <div className="flex items-center gap-1.5 px-2.5 py-1.5 text-xs text-muted">
        <Brain className="h-3 w-3" />
        <span>深度思考中…</span>
      </div>
      <div className="max-h-40 overflow-y-auto px-2.5 pb-2 text-xs leading-relaxed text-muted/90 whitespace-pre-wrap">
        {typed}
      </div>
    </div>
  )
}

function ThinkingFold({ text }: { text?: string | unknown }) {
  // 类型守卫：后端已修复空 thinking 不序列化，但历史数据可能残留空字符串或空对象。
  // 严格验证类型并过滤空值，确保只渲染有效的思考内容。
  if (!text || typeof text !== 'string' || text.trim() === '') return null
  return (
    <details className="group mb-2 self-start rounded-lg border border-border/60 bg-background/40">
      <summary className="flex cursor-pointer select-none items-center gap-1.5 px-2.5 py-1.5 text-xs text-muted">
        <Brain className="h-3 w-3" />
        <span>内部思考</span>
        <ChevronDown className="h-3 w-3 transition-transform group-open:rotate-180" />
      </summary>
      <div className="max-h-56 overflow-y-auto px-3 pb-2.5 text-xs leading-relaxed text-muted/90 whitespace-pre-wrap">
        {text}
      </div>
    </details>
  )
}

function LiveThoughtLane({
  agentName,
  lane,
}: {
  agentName: string
  lane: { thinking: string; text: string }
}) {
  const typed = useTypewriter(lane.text)
  if (agentName === 'assistant') {
    return (
      <div className="flex justify-start pb-5">
        <div className="flex w-full max-w-3xl flex-col items-start">
          {lane.thinking ? <ThinkingStream text={lane.thinking} /> : null}
          <div className="rounded-2xl rounded-tl-md bg-surface-2 px-4 py-2.5 text-sm leading-relaxed text-text">
            {typed}
          </div>
        </div>
      </div>
    )
  }
  return (
    <AgentCard agentName={agentName}>
      {lane.thinking ? <ThinkingStream text={lane.thinking} /> : null}
      <ReasoningCard text={typed} agentName={agentName} streaming />
    </AgentCard>
  )
}
