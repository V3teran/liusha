import { Brain, Braces, ChevronDown } from 'lucide-react'
import { agentProfile } from '@/lib/agentColor'
import { CardHeader } from './CardHeader'
import { Markdown } from './Markdown'

const chipClass = 'rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-text-secondary'

/**
 * 一轮的身份行：谁、在第几步、这一轮用了多少。它是卡的骨架里最先读到的部分——先知道这是谁的卡，
 * 再看它做了什么（工具调用）、想了什么（内部思考）、得出什么（结论），那三样由时间线排在它之后。
 */
export function RoundHeader({
  agentName,
  step,
  inTokens,
  outTokens,
  latencyMs,
  streaming,
}: {
  agentName?: string
  step?: number
  inTokens?: number
  outTokens?: number
  latencyMs?: number
  streaming?: boolean
}) {
  const profile = agentProfile(agentName)
  return (
    <CardHeader
      agent={profile.label || undefined}
      agentColor={profile.accent}
      action={streaming ? '推理中' : '推理'}
      extras={
        <>
          {!!step && (
            <span
              className="rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-xs font-semibold text-text-secondary"
              title="本次指令的第几步推理"
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
  )
}

interface ReasoningCardProps {
  text: string
  thinking?: string | unknown // 类型守卫：应对后端类型异常
  agentName?: string
  streaming?: boolean
}

const fmtTok = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 'k' : String(n)) : '')
const fmtMs = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 's' : n + 'ms') : '')

function looksLikeJson(text: string): boolean {
  const t = text.trim()
  return (t.startsWith('{') && t.endsWith('}')) || (t.startsWith('[') && t.endsWith(']'))
}

function prettyJson(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return text
  }
}

function Fold({ icon: Icon, label, text }: { icon: typeof Brain; label: string; text: string }) {
  return (
    <details className="group mb-2 rounded-lg border border-border/60 bg-background/40">
      <summary className="flex cursor-pointer select-none items-center gap-1.5 px-2.5 py-1.5 text-xs text-text-secondary">
        <Icon className="h-3 w-3" />
        <span>{label}</span>
        <ChevronDown className="h-3 w-3 transition-transform group-open:rotate-180" />
      </summary>
      <div className="max-h-56 overflow-y-auto px-3 pb-2.5 text-xs leading-relaxed text-text-secondary/90 whitespace-pre-wrap">
        {text}
      </div>
    </details>
  )
}

// 一轮的结论：内部思考（折叠）与正文。身份行（RoundHeader）由时间线排在它前面——工具调用之后。
export function ReasoningCard({ text, thinking, agentName, streaming }: ReasoningCardProps) {
  const profile = agentProfile(agentName)
  const jsonBody = !streaming && looksLikeJson(text)

  // 类型守卫：验证 thinking 是有效字符串，过滤空对象/空字符串
  const validThinking = thinking && typeof thinking === 'string' && thinking.trim() !== '' ? thinking : undefined

  return (
    <div data-card="reasoning">
      {!!validThinking && !streaming && <Fold icon={Brain} label="内部思考" text={validThinking} />}
      {jsonBody ? (
        <Fold icon={Braces} label="结构化输出" text={prettyJson(text)} />
      ) : !text.trim() && !streaming ? (
        <div className="text-sm italic text-text-tertiary">（本轮未产出正文，仅有内部思考）</div>
      ) : (
        <div className="text-sm leading-relaxed text-text">
          <Markdown content={text} className="inline" />
          {streaming && (
            <span
              className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse rounded-sm align-text-bottom"
              style={{ background: profile.accent }}
            />
          )}
        </div>
      )}
    </div>
  )
}
