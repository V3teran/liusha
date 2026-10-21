import { Brain, Braces, ChevronDown, FileCode } from 'lucide-react'
import { agentAccent, agentLabel as toLabel } from '@/lib/agentColor'
import { CardHeader } from './CardHeader'
import { Markdown } from './Markdown'

interface ReasoningCardProps {
  text: string
  thinking?: string // 模型扩展思考终稿（metadata.thinking）；折叠区展示，缺省无
  raw?: string // 协议原文（metadata.raw）；正文为解析后叙述时折叠可查
  agentName?: string // 产出该推理的 agent（编排/侦察/利用/流量分析）
  step?: number // 本次用户指令内的全局推理步号（跨 agent 统一计数，追加指令从头）
  inTokens?: number
  outTokens?: number
  latencyMs?: number
  streaming?: boolean // 流式活动节点：显示「推理中」+ 闪烁光标，token/耗时 chip 待最终帧
}

const fmtTok = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 'k' : String(n)) : '')
const fmtMs = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 's' : n + 'ms') : '')

const chipClass = 'rounded-md bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-muted'

// 残留 JSON 正文的判定：对象/数组起收。已知协议（规划/裁决/执行）在后端解析
// 边界转叙述；此处只兜底未覆盖的 JSON 形态（协议漂移/自由输出）——格式化呈现，
// 不解释语义。
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

// 折叠区（内部思考/结构化输出/原始输出共用的展示形态）。
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

// 推理正文（方案 B）：agent 的思路/分析/计划（markdown 富文本）+ step/token/耗时元信息。
// 外层 AgentCard 承载图标（谁在想）+ 脚注复制；卡内领头「agent 推理」+ 元信息 chip。
export function ReasoningCard({ text, thinking, raw, agentName, step, inTokens, outTokens, latencyMs, streaming }: ReasoningCardProps) {
  const accent = agentAccent(agentName)
  const agent = toLabel(agentName)
  const jsonBody = !streaming && looksLikeJson(text)

  return (
    <div data-card="reasoning">
      {!!thinking && !streaming && <Fold icon={Brain} label="内部思考" text={thinking} />}
      {!!raw && !streaming && <Fold icon={FileCode} label="原始输出" text={prettyJson(raw)} />}
      <CardHeader
        agent={agent || undefined}
        agentColor={accent.accent}
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
      {jsonBody ? (
        <Fold icon={Braces} label="结构化输出" text={prettyJson(text)} />
      ) : (
        <div className="text-sm leading-relaxed text-text">
          <Markdown content={text} className="inline" />
          {streaming && (
            <span
              className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse rounded-[1px] align-text-bottom"
              style={{ background: accent.accent }}
            />
          )}
        </div>
      )}
    </div>
  )
}
