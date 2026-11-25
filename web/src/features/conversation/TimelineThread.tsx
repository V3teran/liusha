import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Brain, ChevronDown, Loader2 } from 'lucide-react'
import type { Message } from '@/api/types'
import { useConversationStore } from '@/stores/conversation'
import { dayKey, dayLabel } from '@/lib/format'
import {
  agentActivity,
  authorOf,
  buildThreadRows,
  filterByAgent,
  inFlightToolCalls,
  pairToolResults,
  roundKey,
  type RoundRow,
} from './threadRows'
import { classifyMessage } from './messageKind'
import { useTypewriter } from '@/hooks/useTypewriter'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { AgentCard } from './AgentCard'
import { AgentFilter } from './AgentFilter'
import { StepTools } from './StepTools'
import { useExpansion } from './useExpansion'
import { CommandBlock } from './cards/CommandBlock'
import { ReplyBlock } from './cards/ReplyBlock'
import { ReasoningCard, RoundHeader } from './cards/ReasoningCard'
import { FindingCard } from './cards/FindingCard'
import { TaskStatusCard } from './cards/TaskStatusCard'
import { SteeringCard } from './cards/SteeringCard'

const ROW_DEPS = { dayKey, dayLabel }

function cardCopyText(r: { msg: Message }): string | undefined {
  if (classifyMessage(r.msg) === 'reasoning') return r.msg.metadata?.text || r.msg.content
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
      <div className="inline-flex items-center gap-2 rounded-full border border-border bg-surface-2/60 px-4 py-1.5 text-xs text-text-secondary">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        <span>智能体工作中</span>
        <span className="font-mono text-xs">{label}</span>
      </div>
    </div>
  )
}

/**
 * 对话的执行轨迹：消息流整理成「行」后自上而下渲染。一个智能体的一轮是一张卡——身份、这一轮的
 * 工具调用（折叠）、结论——已出终稿的轮按终稿先后排列；终稿还没到的轮与同一智能体的流式思考
 * 合成一张卡，放在最后，轮出终稿后挪到前面的位置。
 *
 * agent 是筛选：只看某一个智能体（人的指令与系统节点留着做上下文）。
 */
export function TimelineThread({
  active,
  elapsedMs,
  agent,
  onAgentChange,
}: {
  active?: boolean
  elapsedMs?: number
  /** 选中的智能体；null = 全部 */
  agent: string | null
  onAgentChange: (name: string | null) => void
}) {
  const messages = useConversationStore((s) => s.messages)
  const liveStreams = useConversationStore((s) => s.liveStreams)
  const liveOrder = useConversationStore((s) => s.liveOrder)
  const thinking = useConversationStore((s) => s.thinking)
  const expansion = useExpansion()
  const elRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)
  const [unread, setUnread] = useState(0)
  const prevLenRef = useRef(0)
  const { copiedKey, copy } = useCopyToClipboard()

  // 派生值 memo 化：本组件订阅 liveStreams，SSE 每帧 delta 都会重渲染——
  // 全量消息推导（行整理/在途工具/结果配对）不缓存的话每帧 O(N) 重算 + 下游卡片全量重渲染。
  const rows = useMemo(() => buildThreadRows(messages, ROW_DEPS), [messages])
  const runningTools = useMemo(() => (active ? inFlightToolCalls(messages) : undefined), [active, messages])
  const toolPairs = useMemo(() => pairToolResults(messages), [messages])
  const agents = useMemo(() => agentActivity(rows), [rows])

  // 筛选只认这个对话里出现过的智能体：带着筛选切换到没有它的对话时，不生效
  const selected = agent !== null && agents.some((a) => a.name === agent) ? agent : null

  // 已出终稿的行；终稿没到的轮与流式道按智能体合并成卡，排在最后
  const settled = useMemo(() => filterByAgent(rows, selected).filter((r) => r.kind !== 'round'), [rows, selected])
  const inFlight = useMemo(() => {
    // agent → 它在途的轮、首个事件的位置、是否正在流式输出
    const groups = new Map<string, { agent: string; rounds: RoundRow[]; first: number; streaming: boolean }>()
    const groupOf = (name: string) => {
      let g = groups.get(name)
      if (!g) {
        g = { agent: name, rounds: [], first: Number.MAX_SAFE_INTEGER, streaming: false }
        groups.set(name, g)
      }
      return g
    }
    for (const r of rows) {
      if (r.kind === 'round' && (selected === null || r.agent === selected)) {
        const g = groupOf(r.agent)
        g.rounds.push(r)
        g.first = Math.min(g.first, r.tools[0]?.seq ?? g.first)
      }
    }
    for (const lane of liveOrder) {
      if (selected === null || lane === selected) groupOf(lane).streaming = true
    }
    // 只在流式输出、还没调工具的智能体排在后面（稳定排序保持它们的首现顺序）
    return [...groups.values()].sort((a, b) => a.first - b.first)
  }, [rows, liveOrder, selected])
  const activityCount = settled.filter((r) => r.kind !== 'divider').length + inFlight.length

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
    // 会话首绘（切入会话、历史批次落地）：无条件滚到最低——打开对话就该看最新，
    // 不能依赖 nearBottom（长历史从顶部渲染时永不接近底部，粘底逻辑不会触发）。
    const firstPaint = prev === 0 && n > 0
    if (firstPaint) {
      requestAnimationFrame(() => {
        if (elRef.current) elRef.current.scrollTop = elRef.current.scrollHeight
      })
      setAtBottom(true)
      setUnread(0)
    } else if (nearBottom()) {
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
      {agents.length > 0 && <AgentFilter agents={agents} value={selected} onChange={onAgentChange} />}

      <div
        ref={elRef}
        className="flex-1 overflow-y-auto py-4 pl-2 pr-4"
        role="log"
        aria-live="polite"
        aria-relevant="additions"
        aria-label="执行轨迹"
        onScroll={onScroll}
      >
        {settled.map((r) => {
          if (r.kind === 'divider') {
            return (
              <div key={r.key} className="ml-[26px] my-2.5 flex items-center gap-3 text-xs text-text-secondary">
                <span className="h-px flex-1 bg-border" />
                <span className="flex-shrink-0 font-mono tracking-wide">{r.label}</span>
                <span className="h-px flex-1 bg-border" />
              </div>
            )
          }
          const tag = classifyMessage(r.msg)
          if (tag === 'user') {
            return (
              <CommandBlock
                key={r.key}
                content={r.msg.content}
                createdAt={r.msg.created_at}
                attachments={r.msg.metadata?.attachments}
                copied={copiedKey === String(r.msg.seq)}
                onCopy={() => copy(String(r.msg.seq), r.msg.content)}
              />
            )
          }
          if (tag === 'task-status') {
            // 任务生命周期节点 → 居中系统窄条（系统判定无归属）
            return <TaskStatusCard key={r.key} md={r.msg.metadata!} />
          }
          if (tag === 'steering') {
            // 运行中引导的回执 → 同为居中系统窄条（人的操作，不归属任何 agent）
            return <SteeringCard key={r.key} msg={r.msg} />
          }
          if (tag === 'finding') {
            // 坐实的发现 → 系统节点，按严重度着色。它带着评估者的 agent_name 只因为经评估者坐实：
            // 套上 agent 卡会顶着一个「评估」的头像，看起来像评估者的发言
            return (
              <div key={r.key} className="pb-6 pl-10">
                <FindingCard
                  summary={typeof r.msg.metadata?.summary === 'string' ? r.msg.metadata.summary : ''}
                  severity={typeof r.msg.metadata?.severity === 'string' ? r.msg.metadata.severity : 'info'}
                  host={typeof r.msg.metadata?.host === 'string' ? r.msg.metadata.host : undefined}
                  target={typeof r.msg.metadata?.target === 'string' ? r.msg.metadata.target : undefined}
                />
              </div>
            )
          }
          // 聊天回答与任务 agent 的结论同一张卡：头像色环标识身份 + 卡内折叠思考 + 正文
          const name = authorOf(r.msg)
          const copyText = cardCopyText(r)
          return (
            <AgentCard
              key={r.key}
              agentName={name}
              createdAt={r.msg.created_at}
              copied={copiedKey === String(r.msg.seq)}
              onCopy={copyText ? () => copy(String(r.msg.seq), copyText) : undefined}
              copyText={copyText}
            >
              {tag === 'assistant' ? (
                <>
                  <ThinkingFold text={r.msg.metadata?.thinking} />
                  <ReplyBlock content={r.msg.content} agentName={name} />
                </>
              ) : (
                // 一轮由外到内：身份 → 做了什么（工具调用）→ 想了什么 → 得出什么。
                // 工具调用是得出这一条结论的一步，所以排在它自己的结论之前
                <>
                  <RoundHeader
                    agentName={name}
                    step={r.step}
                    inTokens={r.msg.metadata?.in_tokens}
                    outTokens={r.msg.metadata?.out_tokens}
                    latencyMs={r.msg.metadata?.latency_ms}
                  />
                  {r.tools && (
                    <StepTools
                      tools={r.tools}
                      foldKey={roundKey(r.tools)}
                      expansion={expansion}
                      runningSeqs={runningTools}
                      pairs={toolPairs}
                      ended={!active}
                    />
                  )}
                  <ReasoningCard
                    text={r.msg.metadata?.text || r.msg.content}
                    thinking={r.msg.metadata?.thinking || undefined}
                    agentName={name}
                  />
                </>
              )}
            </AgentCard>
          )
        })}

        {thinking && (
          <div className="flex justify-start pb-5" role="status" aria-live="polite" data-thinking-node>
            <div className="flex w-full max-w-3xl flex-col items-start">
              <div className="flex items-center gap-2.5 rounded-2xl rounded-tl-md bg-surface-2 px-4 py-3">
                <span className="flex items-center gap-1">
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-text-tertiary [animation-delay:0ms]" />
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-text-tertiary [animation-delay:160ms]" />
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-text-tertiary [animation-delay:320ms]" />
                </span>
                <span className="text-sm text-text-secondary">正在思考…</span>
              </div>
            </div>
          </div>
        )}

        {/* 流式思考道是用户可见的正文（Markdown 里的 URL 是可聚焦链接），不对读屏隐藏；
            它不是 live region，逐字增量不会被播报，读屏按需浏览即可。 */}
        {inFlight.map((g) => (
          <div key={`live-${g.agent}`} data-live-node={g.streaming ? '' : undefined}>
            {/* 在途卡里只有工具组、还没有文字身份：头像带名字 */}
            <AgentCard agentName={g.agent} nameAvatar>
              {g.rounds.map((r) => (
                <StepTools
                  key={r.key}
                  tools={r.tools}
                  foldKey={r.key}
                  expansion={expansion}
                  runningSeqs={runningTools}
                  pairs={toolPairs}
                  ended={!active}
                  unconcluded={!active}
                />
              ))}
              {g.streaming && <StreamedThought agentName={g.agent} lane={liveStreams[g.agent]} />}
            </AgentCard>
          </div>
        ))}

        {active && inFlight.length === 0 && <RunningIndicator elapsedMs={elapsedMs} />}
      </div>

      {!atBottom && (
        <button
          type="button"
          onClick={scrollToBottom}
          className="absolute inset-x-0 bottom-4 z-10 mx-auto flex w-fit items-center gap-1.5 rounded-full bg-accent-soft px-4 py-1.5 text-sm font-medium text-accent shadow-lg hover:bg-accent/10 hover:text-accent-hover"
        >
          <span>{unread > 0 ? `${unread > 99 ? '99+' : unread} 条新动态` : '回到最新'}</span>
          <span className="text-sm leading-none">↓</span>
        </button>
      )}
    </div>
  )
}

function ThinkingStream({ text }: { text: string | unknown }) {
  // useTypewriter 必须无条件调用（Rules of Hooks——text 由空变非空时 hook 数量
  // 不能变化），由 hasText 决定渲染与否。
  const hasText = !!text && typeof text === 'string' && text.trim() !== ''
  const typed = useTypewriter(hasText ? (text as string) : '')
  return (
    <div className="mb-2 rounded-lg border border-border/60 bg-background/40">
      <div className="flex items-center gap-1.5 px-2.5 py-1.5 text-xs text-text-secondary">
        <Brain className="h-3 w-3" />
        <span>深度思考中…</span>
      </div>
      {/* 可滚动区域须能用键盘聚焦滚动（WCAG 2.1.1） */}
      <div
        tabIndex={0}
        role="region"
        aria-label="深度思考内容"
        className="max-h-40 overflow-y-auto px-2.5 pb-2 text-xs leading-relaxed text-text-secondary/90 whitespace-pre-wrap"
      >
        {typed}
      </div>
    </div>
  )
}

function ThinkingFold({ text }: { text?: string | unknown }) {
  if (!text || typeof text !== 'string' || text.trim() === '') return null
  return (
    <details className="group mb-2 self-start rounded-lg border border-border/60 bg-background/40">
      <summary className="flex cursor-pointer select-none items-center gap-1.5 px-2.5 py-1.5 text-xs text-text-secondary">
        <Brain className="h-3 w-3" />
        <span>内部思考</span>
        <ChevronDown className="h-3 w-3 transition-transform group-open:rotate-180" />
      </summary>
      <div className="max-h-56 overflow-y-auto px-3 pb-2.5 text-xs leading-relaxed text-text-secondary/90 whitespace-pre-wrap">
        {text}
      </div>
    </details>
  )
}

/** 智能体正在流式输出的思考：卡内先是在途的工具调用，再是流式的正文。 */
function StreamedThought({
  agentName,
  lane,
}: {
  agentName: string
  lane: { thinking: string; text: string }
}) {
  const typed = useTypewriter(lane.text)
  // 聊天回答与任务 agent 的流式道同构：先思考道后正文。
  return (
    <>
      {lane.thinking ? <ThinkingStream text={lane.thinking} /> : null}
      {agentName === 'assistant' ? (
        <ReplyBlock content={typed} agentName={agentName} />
      ) : (
        <>
          <RoundHeader agentName={agentName} streaming />
          <ReasoningCard text={typed} agentName={agentName} streaming />
        </>
      )}
    </>
  )
}
