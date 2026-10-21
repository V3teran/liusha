import { useCallback, useEffect, useRef, useState } from 'react'
import { Activity, Bot, Brain, ChevronDown, Loader2, Wrench, type LucideIcon } from 'lucide-react'
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
import { ToolCallCard } from './cards/ToolCallCard'
import { ToolResultCard } from './cards/ToolResultCard'
import { TaskStatusCard } from './cards/TaskStatusCard'

// 每行的 agent 归属：驱动卡外图标配色 + 卡内领头名。
// 取该行动作的施动 agent（Metadata.agent_name）：推理/工具/漏洞归产出者，派发归调度者(编排)；
// tools 组取首条 tool 的 agent。user/assistant 不走此路（各自左右独立块）。
// 四 agent 平级协作（bus 广播、互相制衡），不做主从缩进——身份由配色/图标/领头名承担；
// 缩进语言保留给未来真正的 orchestrator→subagent 派发场景。
function rowAgent(r: ThreadRow): string {
  if (r.kind === 'tools') return r.tools[0]?.Metadata?.agent_name || ''
  if (r.kind === 'msg') return r.msg.Metadata?.agent_name || ''
  return ''
}

// 行图标：每行按语义取一个 lucide 图标（想=Brain、工具=Wrench、状态=Activity、
// 答复=Bot）。图标本身即分类索引，与 AppShell/Composer 的 lucide 语言统一。
function rowIcon(r: ThreadRow): LucideIcon {
  if (r.kind !== 'msg') return Wrench
  const tag = classifyMessage(r.msg)
  switch (tag) {
    case 'reasoning':
      return Brain
    case 'task-status':
      return Activity
    default:
      return Bot
  }
}

// 卡内可复制正文：推理取推理文本，答复走独立块，其余（派发/漏洞/压缩）无脚注复制。
function cardCopyText(r: Exclude<ThreadRow, { kind: 'divider' | 'tools' }>): string | undefined {
  const m = r.msg
  if (classifyMessage(m) === 'reasoning') return m.Metadata?.text || m.Content
  return undefined
}

// 方案 B · 统一活动时间轴：观测「AI 自主作战」。
//   - 用户指令 → 右侧独立指令块（CommandBlock）：accent 软底 + 右上收角 + 脚注时间/复制。
//   - AI 动作（推理/派发/漏洞/压缩）→ 左侧 agent 卡（AgentCard）：图标在卡外(形状=动作、色=agent)
//     + surface 卡框 + 脚注时间/复制，与用户块左右对称、材质等重；子代理右缩一档。
//   - 卡内领头「agent 动作」：agent 名领头(agent 色)、动作词弱化——每节点=某 agent 在做某事。
//   - 工具调用聚成独立缩进行（StepTools），不折进推理卡；答复走左侧 ReplyBlock，与用户块对称。
// RunningIndicator 是任务运行中的间隙指示：任务在跑且当前无任何活动道时，
// 在时间线底部持续跳动（spinner + 墙钟计时）——agent 轮次之间的工具执行/
// 机器复放/事件传播空窗不再死寂（业界 CI job spinner / Claude Code Working 同款
// 范式：只要有活动就没静止）。任何 agent 道开始流时它让位（道本身即活动信号）。
//
// elapsedMs 是后端 usage 的墙钟口径（duration_ms，4s 轮询校准）；本地每秒平滑
// 进位，校准到达时以校准值为新基线——不回跳（取基线推进的单调插值）。
function RunningIndicator({ elapsedMs }: { elapsedMs?: number }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(t)
  }, [])
  const baseRef = useRef<{ ms: number; at: number }>({ ms: elapsedMs ?? 0, at: Date.now() })
  useEffect(() => {
    const prev = baseRef.current
    // 校准仅在前进时重置基线（usage 偶发回退不采纳，计时单调）。
    if ((elapsedMs ?? 0) >= prev.ms) baseRef.current = { ms: elapsedMs ?? 0, at: Date.now() }
  }, [elapsedMs])
  const shown = Math.max(0, baseRef.current.ms + (now - baseRef.current.at))
  const mm = Math.floor(shown / 60000)
  const ss = Math.floor((shown % 60000) / 1000)
  const label = mm >= 60 ? `${Math.floor(mm / 60)}h${mm % 60}m` : `${mm}m${String(ss).padStart(2, '0')}s`
  return (
    <div className="flex justify-center py-2" role="status" aria-live="polite" data-running-indicator>
      <div className="inline-flex items-center gap-2 rounded-full border border-border bg-surface-2/60 px-3.5 py-1.5 text-[12px] text-muted">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        <span>agent 工作中</span>
        <span className="font-mono text-[11px]">{label}</span>
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

  const rows = buildThreadRows(messages, { dayKey, dayLabel })
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
          // agent 答复 → 左侧独立答复块（与用户指令块左右对称）；终稿携带
          // metadata.thinking 时上方渲染折叠思考区（回看历史同样可见）。
          if (r.kind === 'msg' && classifyMessage(r.msg) === 'assistant') {
            return (
              <div key={r.key} className="flex justify-start pb-[18px]">
                <div className="flex max-w-[80%] flex-col items-start">
                  <ThinkingFold text={r.msg.Metadata?.thinking} />
                  <ReplyBlock
                    content={r.msg.Content}
                    agentName={r.msg.Metadata?.agent_name}
                    createdAt={r.msg.CreatedAt}
                    copied={copiedKey === r.msg.ID}
                    onCopy={() => copy(r.msg.ID, r.msg.Content)}
                  />
                </div>
              </div>
            )
          }
          // 任务生命周期节点 → 居中系统窄条（不套 agent 卡——系统裁决无归属）。
          if (r.kind === 'msg' && classifyMessage(r.msg) === 'task-status') {
            return <TaskStatusCard key={r.key} md={r.msg.Metadata!} />
          }
          // 工具组 → 独立缩进行（工具调用不是推理，不进 agent 卡）；子代理工具组再右缩一档。
          if (r.kind === 'tools') {
            return <StepTools key={r.key} tools={r.tools} />
          }
          // AI 动作（推理/派发/漏洞/压缩）→ 左侧 agent 卡（图标在卡外 + surface 卡框 + 脚注时间/复制）。
          const agent = rowAgent(r)
          const color = agentAccent(agent)
          const Icon = rowIcon(r)
          const copyText = cardCopyText(r)
          return (
            <AgentCard
              key={r.key}
              icon={Icon}
              accent={color.accent}
              soft={color.soft}
              createdAt={r.msg.CreatedAt}
              copied={copiedKey === r.msg.ID}
              onCopy={copyText ? () => copy(r.msg.ID, copyText) : undefined}
              copyText={copyText}
            >
              {renderCard(r)}
            </AgentCard>
          )
        })}

        {/* 「正在思考」占位：用户消息发出 → 首个 AI 信号之间的空窗指示（意图分类 +
            首 token 延迟可达十几秒，无占位则无从区分「在干活」与「挂了」）。
            首个增量帧或 assistant 消息落地即被 store 清除，与活动气泡自然衔接。
            与 ReplyBlock 同构（左侧 surface-2 气泡、左上收角），三点错拍跳动。 */}
        {thinking && (
          <div className="flex justify-start pb-[18px]" role="status" aria-live="polite" data-thinking-node>
            <div className="flex max-w-[80%] flex-col items-start">
              <div className="flex items-center gap-2.5 rounded-2xl rounded-tl-md bg-surface-2 px-4 py-3">
                <span className="flex items-center gap-1">
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted [animation-delay:0ms]" />
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted [animation-delay:160ms]" />
                  <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-muted [animation-delay:320ms]" />
                </span>
                <span className="text-[12.5px] text-muted">正在思考…</span>
              </div>
            </div>
          </div>
        )}

        {/* 流式活动节点：每条道一个气泡（四 agent 并发 + 聊天助手），thinking 通道
            折叠思考区 + text 通道打字机，各自逐字揭示。
            aria-hidden：内容随后由终态卡完整呈现，避免读屏重复播报。 */}
        {liveOrder.map((lane) => (
          <div key={lane} data-live-node aria-hidden="true">
            <LiveThoughtLane agentName={lane} lane={liveStreams[lane]} />
          </div>
        ))}

        {/* 间隙运行指示：任务在跑且无任何活动道（agent 轮次之间的工具执行/
            复放/唤醒空窗）时持续跳动；有道时让位，终态后消失。 */}
        {active && liveOrder.length === 0 && <RunningIndicator elapsedMs={elapsedMs} />}
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
      const text = md?.text || m.Content
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

// ThinkingStream 是扩展思考折叠区：模型内部 CoT 的流式展示（业界范式——流式期间
// 可见逐字、限定高度滚动防挤占正文、终稿由落定卡的折叠区接管）。原文直出，
// 不做假动画——打字速度即模型真实 token 速度。
function ThinkingStream({ text }: { text: string }) {
  const typed = useTypewriter(text)
  return (
    <div className="mb-2 rounded-lg border border-border/60 bg-background/40">
      <div className="flex items-center gap-1.5 px-2.5 py-1.5 text-[11.5px] text-muted">
        <Brain className="h-3 w-3" />
        <span>深度思考中…</span>
      </div>
      <div className="max-h-40 overflow-y-auto px-2.5 pb-2 text-[12px] leading-relaxed text-muted/90 whitespace-pre-wrap">
        {typed}
      </div>
    </div>
  )
}

// ThinkingFold 是思考终稿折叠区（落库回看形态）：默认收起成「内部思考」，
// 点击展开（业界 Claude「已思考 X 秒」同款交互）。
function ThinkingFold({ text }: { text?: string }) {
  if (!text) return null
  return (
    <details className="group mb-2 self-start rounded-lg border border-border/60 bg-background/40">
      <summary className="flex cursor-pointer select-none items-center gap-1.5 px-2.5 py-1.5 text-[11.5px] text-muted">
        <Brain className="h-3 w-3" />
        <span>内部思考</span>
        <ChevronDown className="h-3 w-3 transition-transform group-open:rotate-180" />
      </summary>
      <div className="max-h-56 overflow-y-auto px-3 pb-2.5 text-[12px] leading-relaxed text-muted/90 whitespace-pre-wrap">
        {text}
      </div>
    </details>
  )
}

// LiveThoughtLane 是一条流式活动道：thinking 通道渲染折叠思考区（先流）、text
// 通道渲染打字机（后流）；assistant 道是聊天助手（与 ReplyBlock 同形态），agent
// 道与落定推理卡同布局（AgentCard 包裹）。
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
      <div className="flex justify-start pb-[18px]">
        <div className="flex max-w-[80%] flex-col items-start">
          {lane.thinking ? <ThinkingStream text={lane.thinking} /> : null}
          <div className="rounded-2xl rounded-tl-md bg-surface-2 px-3.5 py-2.5 text-sm leading-relaxed text-text">
            {typed}
          </div>
        </div>
      </div>
    )
  }
  const color = agentAccent(agentName)
  return (
    <AgentCard icon={Brain} accent={color.accent} soft={color.soft}>
      {lane.thinking ? <ThinkingStream text={lane.thinking} /> : null}
      <ReasoningCard text={typed} agentName={agentName} streaming />
    </AgentCard>
  )
}
