import { agentAccent, agentLabel as toLabel } from '@/lib/agentColor'
import { Markdown } from './Markdown'

interface ReasoningCardProps {
  text: string
  agentName?: string // 产出该推理的 agent（orchestrator/exploitation/reconnaissance）
  step?: number // 本次用户指令内的全局推理步号（跨 agent 统一计数，追加指令从头）
  inTokens?: number
  outTokens?: number
  latencyMs?: number
  streaming?: boolean // 流式活动气泡：显示「推理中」+ 闪烁光标，token/耗时 chip 待最终帧
}

const fmtTok = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 'k' : String(n)) : '')
const fmtMs = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 's' : n + 'ms') : '')

// 推理卡：agent 的思路/分析/计划/决策（markdown 富文本）+ 本次 LLM 交互的 token/耗时元信息。
export function ReasoningCard({
  text,
  agentName,
  step,
  inTokens,
  outTokens,
  latencyMs,
  streaming,
}: ReasoningCardProps) {
  const hasMeta = (inTokens || 0) > 0 || (latencyMs || 0) > 0
  // agent 英文 id → 中文标签（编排/侦察/利用/流量分析），未知名回退原始 id。
  const agentLabel = toLabel(agentName)
  // 每个 agent 独立色（紫=指挥/青=侦察/玫红=利用…），驱动边框+标题+标签+光标。
  const accent = agentAccent(agentName)

  return (
    <div
      data-card="reasoning"
      style={{ '--rc-accent': accent.accent, '--rc-accent-soft': accent.soft } as React.CSSProperties}
      className="self-start max-w-[88%] rounded-xl border border-border bg-surface px-3.5 py-2.5 shadow-sm"
    >
      <div
        className="mb-1 flex items-center gap-1.5 border-l-[3px] pl-0"
        style={{ borderColor: accent.accent }}
      />
      <div className="flex items-center gap-1.5 text-xs">
        <span>🧠</span>
        <span className="font-semibold" style={{ color: accent.accent }}>
          {streaming ? '推理中' : '推理'}
        </span>
        {agentLabel && (
          <span
            className="rounded px-1.5 py-0.5 text-[11px] font-semibold"
            style={{ color: accent.accent, background: accent.soft }}
          >
            {agentLabel}
          </span>
        )}
        {!!step && (
          <span
            className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-muted"
            title="本次指令的第几步推理（全局计数，追加指令从头）"
          >
            第 {step} 步
          </span>
        )}
        {hasMeta && (
          <span className="ml-auto flex gap-1.5">
            {(inTokens || 0) > 0 && (
              <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted" title="输入 token">
                ↑ {fmtTok(inTokens)}
              </span>
            )}
            {(outTokens || 0) > 0 && (
              <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted" title="输出 token">
                ↓ {fmtTok(outTokens)}
              </span>
            )}
            {(latencyMs || 0) > 0 && (
              <span className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted" title="耗时">
                ⏱ {fmtMs(latencyMs)}
              </span>
            )}
          </span>
        )}
      </div>
      <Markdown content={text} className="inline text-sm leading-relaxed text-text" />
      {streaming && (
        <span
          className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse rounded-[1px] align-text-bottom"
          style={{ background: accent.accent }}
        />
      )}
    </div>
  )
}
