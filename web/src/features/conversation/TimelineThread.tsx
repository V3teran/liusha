import { useCallback, useEffect, useRef, useState } from 'react'
import { Bot, Bug, Rocket, Brain, Wrench, Archive, type LucideIcon } from 'lucide-react'
import { useConversationStore } from '@/stores/conversation'
import { dayKey, dayLabel } from '@/lib/format'
import { buildThreadRows, type ThreadRow } from '@/lib/threadRows'
import { classifyMessage } from '@/lib/messageKind'
import { agentAccent } from '@/lib/agentColor'
import { useTypewriter } from '@/hooks/useTypewriter'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { AgentCard } from './AgentCard'
import { StepTools } from './StepTools'
import { CommandBlock } from './cards/CommandBlock'
import { ReplyBlock } from './cards/ReplyBlock'
import { ReasoningCard } from './cards/ReasoningCard'
import { SpawnCard } from './cards/SpawnCard'
import { ToolCallCard } from './cards/ToolCallCard'
import { ToolResultCard } from './cards/ToolResultCard'
import { FindingCard } from './cards/FindingCard'
import { CompactionCard } from './cards/CompactionCard'

// 每行的 hunter 归属：驱动缩进（子代理右缩一档）+ 卡外图标配色 + 卡内领头名。
// 取该行动作的施动 hunter（Metadata.AgentName）：推理/工具/漏洞归产出者，派发归调度者(编排)；
// tools 组取首条 tool 的 hunter。user/assistant 不走此路（各自左右独立块）。
function rowAgent(r: ThreadRow): string {
  if (r.kind === 'tools') return r.tools[0]?.Metadata?.AgentName || ''
  if (r.kind === 'msg') return r.msg.Metadata?.AgentName || ''
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

// 卡内可复制正文：推理取推理文本，答复走独立块，其余（派发/漏洞/压缩）无脚注复制。
function cardCopyText(r: Exclude<ThreadRow, { kind: 'divider' | 'tools' }>): string | undefined {
  const m = r.msg
  if (classifyMessage(m) === 'reasoning') return m.Metadata?.Text || m.Content
  return undefined
}

// 方案 B · 统一活动时间轴：观测「AI 自主作战」。
//   - 用户指令 → 右侧独立指令块（CommandBlock）：accent 软底 + 右上收角 + 脚注时间/复制。
//   - AI 动作（推理/派发/漏洞/压缩）→ 左侧 agent 卡（AgentCard）：图标在卡外(形状=动作、色=hunter)
//     + surface 卡框 + 脚注时间/复制，与用户块左右对称、材质等重；子代理右缩一档。
//   - 卡内领头「hunter 动作」：hunter 名领头(hunter 色)、动作词弱化——每节点=某 hunter 在做某事。
//   - 工具调用聚成独立缩进行（StepTools），不折进推理卡；答复走左侧 ReplyBlock，与用户块对称。
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
        aria-label="执行轨迹"
        onScroll={onScroll}
      >
        {rows.map((r) => {
          if (r.kind === 'divider') {
            return (
              <div key={r.key} className="ml-[26px] my-2.5 flex items-center gap-3 text-[11.5px] text-muted">
                <span className="h-px flex-1 bg-border" />
                <span className="flex-shrink-0 font-mono tracking-wide">{r.label}</span>
                <span className="h-px flex-1 bg-border" />
              </div>
            )
          }
          // 用户指令 → 右侧独立指令块（与左侧 agent 卡左右对称）。
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
          // agent 答复 → 左侧独立答复块（与用户指令块左右对称）。
          if (r.kind === 'msg' && classifyMessage(r.msg) === 'assistant') {
            return (
              <ReplyBlock
                key={r.key}
                content={r.msg.Content}
                agentName={r.msg.Metadata?.AgentName}
                createdAt={r.msg.CreatedAt}
                copied={copiedKey === r.msg.ID}
                onCopy={() => copy(r.msg.ID, r.msg.Content)}
              />
            )
          }
          // 工具组 → 独立缩进行（工具调用不是推理，不进 agent 卡）；子代理工具组再右缩一档。
          if (r.kind === 'tools') {
            return <StepTools key={r.key} tools={r.tools} sub={isSubAgent(rowAgent(r), hasOrchestrator)} />
          }
          // AI 动作（推理/派发/漏洞/压缩）→ 左侧 agent 卡（图标在卡外 + surface 卡框 + 脚注时间/复制）。
          const agent = rowAgent(r)
          const sub = isSubAgent(agent, hasOrchestrator)
          const color = agentAccent(agent)
          const Icon = rowIcon(r)
          const copyText = cardCopyText(r)
          return (
            <AgentCard
              key={r.key}
              icon={Icon}
              accent={color.accent}
              soft={color.soft}
              sub={sub}
              createdAt={r.msg.CreatedAt}
              copied={copiedKey === r.msg.ID}
              onCopy={copyText ? () => copy(r.msg.ID, copyText) : undefined}
              copyText={copyText}
            >
              {renderCard(r)}
            </AgentCard>
          )
        })}

        {/* 流式活动节点：与落定节点同布局（agent 卡），逐字打字机揭示 */}
        {liveReasoning && (
          <div data-live-node aria-hidden="true">
            <AgentCard icon={Brain} accent={liveColor.accent} soft={liveColor.soft} sub={liveIsSub}>
              <ReasoningCard text={typedReasoning} agentName={liveAgentName || undefined} streaming />
            </AgentCard>
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

// agent 卡内容：按分类渲染对应卡片。复制/时间由外层 AgentCard 脚注承担（与用户块一致）。
// tools 组不入此（map 里直接 StepTools 独立成行）；user/assistant 各走 CommandBlock/ReplyBlock。
function renderCard(r: Exclude<ThreadRow, { kind: 'divider' | 'tools' }>) {
  const m = r.msg
  const md = m.Metadata
  const tag = classifyMessage(m)

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
        />
      )
    }
    case 'spawn':
      return <SpawnCard dispatcher={md?.AgentName} args={md?.Args} />
    case 'spawn-done':
      return <SpawnCard done dispatcher={md?.AgentName} durationMs={md?.DurationMs} err={md?.Err} />
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
