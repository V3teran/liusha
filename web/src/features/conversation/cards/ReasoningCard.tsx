import type { ReactNode } from 'react'
import { agentAccent, agentLabel as toLabel } from '@/lib/agentColor'
import { Markdown } from './Markdown'

interface ReasoningCardProps {
  text: string
  agentName?: string // 产出该推理的 agent（orchestrator/exploitation/reconnaissance）
  step?: number // 本次用户指令内的全局推理步号（跨 agent 统一计数，追加指令从头）
  inTokens?: number
  outTokens?: number
  latencyMs?: number
  streaming?: boolean // 流式活动节点：显示「推理中」+ 闪烁光标，token/耗时 chip 待最终帧
  copySlot?: ReactNode // 复制按钮（落定态由 TimelineThread 注入；流式态不传）
}

const fmtTok = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 'k' : String(n)) : '')
const fmtMs = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 's' : n + 'ms') : '')

// 推理文字流（方案 A）：agent 的思路/分析/计划（markdown 富文本）+ token/耗时元信息。
// 无卡片外壳——外层 RailNode 的图标点已承载「谁在想」的身份，这里是导轨上的纯文字流，
// 信息密度高、不再是气泡。头行：推理 + agent 标签 + step + token/耗时 chip + 复制。
export function ReasoningCard({
  text,
  agentName,
  step,
  inTokens,
  outTokens,
  latencyMs,
  streaming,
  copySlot,
}: ReasoningCardProps) {
  const hasMeta = (inTokens || 0) > 0 || (latencyMs || 0) > 0
  const agentLabel = toLabel(agentName)
  const accent = agentAccent(agentName)

  return (
    <div data-card="reasoning">
      <div className="mb-1 flex flex-wrap items-center gap-1.5 text-xs">
        <span className="font-semibold" style={{ color: accent.accent }}>
          {streaming ? '推理中' : '推理'}
        </span>
        {agentLabel && (
          <span
            className="rounded-md px-1.5 py-0.5 text-[11px] font-semibold"
            style={{ color: accent.accent, background: accent.soft }}
          >
            {agentLabel}
          </span>
        )}
        {!!step && (
          <span
            className="rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-muted"
            title="本次指令的第几步推理（全局计数，追加指令从头）"
          >
            第 {step} 步
          </span>
        )}
        {hasMeta && (
          <>
            {(inTokens || 0) > 0 && (
              <span className="rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted" title="输入 token">
                ↑ {fmtTok(inTokens)}
              </span>
            )}
            {(outTokens || 0) > 0 && (
              <span className="rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted" title="输出 token">
                ↓ {fmtTok(outTokens)}
              </span>
            )}
            {(latencyMs || 0) > 0 && (
              <span className="rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted" title="耗时">
                ⏱ {fmtMs(latencyMs)}
              </span>
            )}
          </>
        )}
        {copySlot && <span className="ml-auto">{copySlot}</span>}
      </div>
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
