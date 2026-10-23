import { Brain, Braces, ChevronDown } from 'lucide-react'
import { agentProfile } from '@/lib/agentColor'
import { CardHeader } from './CardHeader'
import { Markdown } from './Markdown'

interface ReasoningCardProps {
  text: string
  thinking?: string | unknown  // 类型守卫：应对后端类型异常
  agentName?: string
  step?: number
  inTokens?: number
  outTokens?: number
  latencyMs?: number
  streaming?: boolean
}

const fmtTok = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 'k' : String(n)) : '')
const fmtMs = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 's' : n + 'ms') : '')

const chipClass = 'rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted'

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
      <summary className="flex cursor-pointer select-none items-center gap-1.5 px-2.5 py-1.5 text-[11.5px] text-muted">
        <Icon className="h-3 w-3" />
        <span>{label}</span>
        <ChevronDown className="h-3 w-3 transition-transform group-open:rotate-180" />
      </summary>
      <div className="max-h-56 overflow-y-auto px-3 pb-2.5 text-[12px] leading-relaxed text-muted/90 whitespace-pre-wrap">
        {text}
      </div>
    </details>
  )
}

export function ReasoningCard({ text, thinking, agentName, step, inTokens, outTokens, latencyMs, streaming }: ReasoningCardProps) {
  const profile = agentProfile(agentName)
  const jsonBody = !streaming && looksLikeJson(text)

  // 类型守卫：验证 thinking 是有效字符串，过滤空对象/空字符串
  const validThinking = thinking && typeof thinking === 'string' && thinking.trim() !== '' ? thinking : undefined

  return (
    <div data-card="reasoning">
      {!!validThinking && !streaming && <Fold icon={Brain} label="内部思考" text={validThinking} />}
      <CardHeader
        agent={profile.label || undefined}
        agentColor={profile.accent}
        action={streaming ? '推理中' : '推理'}
        extras={
          <>
            {!!step && (
              <span
                className="rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-muted"
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
      {jsonBody ? (
        <Fold icon={Braces} label="结构化输出" text={prettyJson(text)} />
      ) : !text.trim() && !streaming ? (
        <div className="text-[12.5px] italic text-faint">（本轮未产出正文，仅有内部思考）</div>
      ) : (
        <div className="text-sm leading-relaxed text-text">
          <Markdown content={text} className="inline" />
          {streaming && (
            <span
              className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse rounded-[1px] align-text-bottom"
              style={{ background: profile.accent }}
            />
          )}
        </div>
      )}
    </div>
  )
}
