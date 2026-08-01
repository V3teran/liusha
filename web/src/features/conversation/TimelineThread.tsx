import { useCallback, useEffect, useRef, useState } from 'react'
import { Bot, Bug, Rocket, Brain, Wrench, Archive, type LucideIcon } from 'lucide-react'
import { useConversationStore } from '@/stores/conversation'
import { dayKey, dayLabel, clockTime, fullTime } from '@/lib/format'
import { buildThreadRows, type ThreadRow } from '@/lib/threadRows'
import { classifyMessage } from '@/lib/messageKind'
import { agentAccent } from '@/lib/agentColor'
import { useTypewriter } from '@/hooks/useTypewriter'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { CopyButton } from '@/components/CopyButton'
import { RailNode } from './RailNode'
import { StepTools } from './StepTools'
import { CommandBlock } from './cards/CommandBlock'
import { ReasoningCard } from './cards/ReasoningCard'
import { SpawnCard } from './cards/SpawnCard'
import { ToolCallCard } from './cards/ToolCallCard'
import { ToolResultCard } from './cards/ToolResultCard'
import { FindingCard } from './cards/FindingCard'
import { CompactionCard } from './cards/CompactionCard'
import { Markdown } from './cards/Markdown'

// 每行的 agent 归属：驱动缩进（非 orchestrator = 子代理，缩进 + 配色连接线）+ 图标点配色。
// reasoning/tool 取 Metadata.AgentName；tools 组取首条 tool 的 agent；其余（user/finding/派发）归 root。
function rowAgent(r: ThreadRow): string {
  if (r.kind === 'tools') return r.tools[0]?.Metadata?.AgentName || ''
  if (r.kind === 'msg') {
    const tag = classifyMessage(r.msg)
    if (tag === 'reasoning' || tag === 'tool-call' || tag === 'tool-result') {
      return r.msg.Metadata?.AgentName || ''
    }
  }
  return ''
}

// 子代理缩进：缩进表达「从属于某个调度者」，而非「名字不是 orchestrator」。
// 仅在编排模式（会话里出现过 orchestrator 派发子代理）下，非 orchestrator 的 agent 才缩进一档 +
// 配色连接线。passive/chatmodel 单 agent 模式没有调度者，主 agent（如 traffic-analysis）不缩进。
function isSubAgent(agent: string, hasOrchestrator: boolean): boolean {
  return hasOrchestrator && !!agent && agent !== 'orchestrator'
}

// 行图标：每行按语义取一个 lucide 图标（想=Brain、工具=Wrench、派发=Rocket、漏洞=Bug、
// 压缩=Archive、答复=Bot）。图标本身即分类索引，与 AppShell/Composer 的 lucide 语言统一。
function rowIcon(r: ThreadRow): LucideIcon {
  if (r.kind !== 'msg') return Wrench
  const tag = classifyMessage(r.msg)
  switch (tag) {
    case 'reasoning':
      return Brain
    case 'spawn':
    case 'spawn-done':
      return Rocket
    case 'finding':
      return Bug
    case 'compaction':
      return Archive
    case 'tool-call':
    case 'tool-result':
      return Wrench
    default:
      return Bot
  }
}

// 是否走左侧作战导轨（AI 过程/答复节点）；用户指令走右侧独立块（CommandBlock），不在导轨上。
function isRailRow(r: ThreadRow): boolean {
  if (r.kind === 'tools') return true
  if (r.kind === 'divider') return false
  return classifyMessage(r.msg) !== 'user'
}

// 方案 A · 统一活动时间轴：观测「AI 自主作战」。
//   - 用户指令 → 右侧独立指令块（CommandBlock），与 AI 流分置左右，材质区分。
//   - AI 全部动作（想/派发/工具/漏洞/答复）→ 左侧图标导轨节点（RailNode），图标点即身份 +
//     分类，节点间以中性细脊连接；子代理（非 orchestrator）缩进一档 + 该 agent 配色连接线。
//   - 推理/答复注入紧凑复制按钮（hover 显现），过程卡直接渲染，无气泡/头像双系统。
export function TimelineThread() {
  const messages = useConversationStore((s) => s.messages)
  const liveReasoning = useConversationStore((s) => s.liveReasoning)
  const liveAgentName = useConversationStore((s) => s.liveAgentName)
  const elRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)
  const [unread, setUnread] = useState(0)
  const prevLenRef = useRef(0)
  const { copiedKey, copy } = useCopyToClipboard()

  const typedReasoning = useTypewriter(liveReasoning)
  const rows = buildThreadRows(messages, { dayKey, dayLabel })
  // 是否编排模式：会话里出现过 orchestrator 归属的消息 = 存在调度者，子代理才缩进。
  // passive/chatmodel 单 agent 模式无 orchestrator，主 agent 平铺不缩进。流式态也纳入判断。
  const hasOrchestrator =
    liveAgentName === 'orchestrator' || messages.some((m) => m.Metadata?.AgentName === 'orchestrator')
  // 未读计数以「渲染行(动态)」为单位（非原始消息条数）：一次推理+工具调用后端落 3 条消息，
  // 渲染只成 2 行（想 + 折叠工具组），数原始消息会与用户实际看到的行数对不上。divider 不计。
  const activityCount = rows.reduce((n, r) => (r.kind === 'divider' ? n : n + 1), 0)
  // 导轨脊线连接判定：最后一个导轨节点之后无节点（且无流式节点）→ 收尾不画向下延伸的脊线。
  let lastRailIdx = -1
  rows.forEach((r, i) => {
    if (isRailRow(r)) lastRailIdx = i
  })

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
  }, [typedReasoning, stickToBottom])

  const liveIsSub = isSubAgent(liveAgentName, hasOrchestrator)
  const liveColor = agentAccent(liveAgentName)

  return (
    <div className="relative flex min-h-0 flex-1 flex-col">
      <div
        ref={elRef}
        className="flex-1 overflow-y-auto py-4 pl-2 pr-4"
        role="log"
        aria-live="polite"
        aria-relevant="additions"
        aria-label="作战轨迹"
        onScroll={onScroll}
      >
        {rows.map((r, i) => {
          if (r.kind === 'divider') {
            return (
              <div key={r.key} className="ml-[26px] my-2.5 flex items-center gap-3 text-[11.5px] text-muted">
                <span className="h-px flex-1 bg-border" />
                <span className="flex-shrink-0 font-mono tracking-wide">{r.label}</span>
                <span className="h-px flex-1 bg-border" />
              </div>
            )
          }
          // 用户指令 → 右侧独立指令块（不在左侧导轨上）。
          if (r.kind === 'msg' && classifyMessage(r.msg) === 'user') {
            return (
              <CommandBlock
                key={r.key}
                content={r.msg.Content}
                createdAt={r.msg.CreatedAt}
                copied={copiedKey === r.msg.ID}
                onCopy={() => copy(r.msg.ID, r.msg.Content)}
              />
            )
          }
          // AI 动作 → 左侧图标导轨节点。
          const agent = rowAgent(r)
          const sub = isSubAgent(agent, hasOrchestrator)
          const color = agentAccent(agent)
          const Icon = rowIcon(r)
          const last = i === lastRailIdx && !liveReasoning
          return (
            <RailNode key={r.key} icon={Icon} accent={color.accent} soft={color.soft} sub={sub} last={last}>
              {renderCard(r, { copiedKey, copy })}
            </RailNode>
          )
        })}

        {/* 流式活动节点：与落定节点同布局（图标导轨 + 缩进），逐字打字机揭示 */}
        {liveReasoning && (
          <div data-live-node aria-hidden="true">
            <RailNode icon={Brain} accent={liveColor.accent} soft={liveColor.soft} sub={liveIsSub} last>
              <ReasoningCard text={typedReasoning} agentName={liveAgentName || undefined} streaming />
            </RailNode>
          </div>
        )}
      </div>

      {!atBottom && (
        <button
          type="button"
          onClick={scrollToBottom}
          className="absolute bottom-4 left-1/2 z-10 flex -translate-x-1/2 items-center gap-1.5 rounded-full bg-accent px-3.5 py-1.5 text-[12.5px] font-medium text-white shadow-lg hover:bg-accent-hover"
        >
          <span>{unread > 0 ? `${unread > 99 ? '99+' : unread} 条新动态` : '回到最新'}</span>
          <span className="text-[13px] leading-none">↓</span>
        </button>
      )}
    </div>
  )
}

interface CopyCtx {
  copiedKey: string | null
  copy: (key: string, text: string) => void
}

// 导轨节点内容：按分类渲染对应卡片。推理/答复注入 hover 复制按钮（业界通行的可发现复制）。
function renderCard(r: Exclude<ThreadRow, { kind: 'divider' }>, { copiedKey, copy }: CopyCtx) {
  if (r.kind === 'tools') return <StepTools tools={r.tools} />

  const m = r.msg
  const md = m.Metadata
  const tag = classifyMessage(m)

  const copyBtn = (text: string) => (
    <CopyButton
      compact
      copied={copiedKey === m.ID}
      onClick={() => copy(m.ID, text)}
      className="opacity-0 transition-opacity focus-visible:opacity-100 group-hover:opacity-100"
    />
  )

  switch (tag) {
    case 'reasoning': {
      const text = md?.Text || m.Content
      return (
        <ReasoningCard
          text={text}
          agentName={md?.AgentName}
          step={r.step}
          inTokens={md?.InTokens}
          outTokens={md?.OutTokens}
          latencyMs={md?.LatencyMs}
          copySlot={copyBtn(text)}
        />
      )
    }
    case 'assistant':
      return (
        <div data-card="assistant">
          <div className="mb-1 flex items-center gap-1.5 text-xs">
            <span className="font-semibold text-text">答复</span>
            {m.CreatedAt && (
              <time className="font-mono text-[11px] leading-none text-faint" dateTime={m.CreatedAt} title={fullTime(m.CreatedAt)}>
                {clockTime(m.CreatedAt)}
              </time>
            )}
            <span className="ml-auto">{copyBtn(m.Content)}</span>
          </div>
          <Markdown content={m.Content} className="text-sm leading-relaxed text-text" />
        </div>
      )
    case 'spawn':
      return <SpawnCard args={md?.Args} />
    case 'spawn-done':
      return <SpawnCard done durationMs={md?.DurationMs} err={md?.Err} />
    case 'finding':
      return <FindingCard args={md?.Args || ''} />
    case 'compaction':
      return <CompactionCard label={md?.Result || ''} summary={md?.Text || ''} />
    case 'tool-call':
      return <ToolCallCard tool={md?.ToolName || ''} args={md?.Args || ''} agentName={md?.AgentName} />
    case 'tool-result':
      return (
        <ToolResultCard
          tool={md?.ToolName || ''}
          result={md?.Result || ''}
          durationMs={md?.DurationMs || 0}
          err={md?.Err || ''}
          agentName={md?.AgentName}
        />
      )
    default:
      return null
  }
}
