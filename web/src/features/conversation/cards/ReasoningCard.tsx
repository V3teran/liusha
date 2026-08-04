import { agentAccent, agentLabel as toLabel } from '@/lib/agentColor'
import { CardHeader } from './CardHeader'
import { Markdown } from './Markdown'

interface ReasoningCardProps {
  text: string
  agentName?: string // 产出该推理的 hunter（编排/侦察/利用/流量分析）
  step?: number // 本次用户指令内的全局推理步号（跨 hunter 统一计数，追加指令从头）
  inTokens?: number
  outTokens?: number
  latencyMs?: number
  streaming?: boolean // 流式活动节点：显示「推理中」+ 闪烁光标，token/耗时 chip 待最终帧
}

const fmtTok = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 'k' : String(n)) : '')
const fmtMs = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 's' : n + 'ms') : '')

const chipClass = 'rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted'

// 推理正文（方案 B）：hunter 的思路/分析/计划（markdown 富文本）+ step/token/耗时元信息。
// 外层 AgentCard 承载图标（谁在想）+ 脚注复制；卡内领头「hunter 推理」+ 元信息 chip。
export function ReasoningCard({ text, agentName, step, inTokens, outTokens, latencyMs, streaming }: ReasoningCardProps) {
  const accent = agentAccent(agentName)
  const hunter = toLabel(agentName)

  return (
    <div data-card="reasoning">
      <CardHeader
        hunter={hunter || undefined}
        hunterColor={accent.accent}
        action={streaming ? '推理中' : '推理'}
        extras={
          <>
            {!!step && (
              <span
                className="rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-muted"
                title="本次指令的第几步推理（全局计数，追加指令从头）"
              >
                第 {step} 步
              </span>
            )}
            {(inTokens || 0) > 0 && (
              <span className={chipClass} title="输入 token">
                ↑ {fmtTok(inTokens)}
              </span>
            )}
            {(outTokens || 0) > 0 && (
              <span className={chipClass} title="输出 token">
                ↓ {fmtTok(outTokens)}
              </span>
            )}
            {(latencyMs || 0) > 0 && (
              <span className={chipClass} title="耗时">
                ⏱ {fmtMs(latencyMs)}
              </span>
            )}
          </>
        }
      />
      <div className="text-sm leading-relaxed text-text">
        <Markdown content={text} className="inline" />
        {streaming && (
          <span
            className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse rounded-[1px] align-text-bottom"
            style={{ background: accent.accent }}
          />
        )}
      </div>
    </div>
  )
}
