import { ChevronRight, Loader2 } from 'lucide-react'
import type { Message } from '@/api/types'
import { agentProfile } from '@/lib/agentColor'
import { cn } from '@/lib/utils'
import { ToolCallCard, type ToolResultInfo } from './cards/ToolCallCard'
import { ToolResultCard } from './cards/ToolResultCard'
import type { Expansion } from './useExpansion'

interface StepToolsProps {
  /** 这一轮的工具过程事件（调用与结果） */
  tools: Message[]
  /** 这一轮的折叠组在时间线里的标识（见 threadRows 的 roundKey） */
  foldKey: string
  expansion: Expansion
  /** 执行中的调用：已发 tool_call 未见配对 tool_result（任务活跃时才有） */
  runningSeqs?: Set<number>
  pairs?: { matched: Map<number, Message>; matchedResultSeqs: Set<number> }
  /** 任务已终态：悬挂 call（有 call 无 result）按「已中断」呈现而非脉冲点 */
  ended?: boolean
  /** 这一轮没有等到终稿：组头说明它没有结论 */
  unconcluded?: boolean
}

/**
 * 一轮里的工具调用：折叠成一行组头（次数 + 工具名预览），展开后一次调用一张卡（调用与结果同卡）。
 * 它放在所属智能体的轮卡里、结论之前——先做了什么，再是得出了什么。
 */
export function StepTools({ tools, foldKey, expansion, runningSeqs, pairs, ended, unconcluded }: StepToolsProps) {
  const accent = agentProfile(tools[0]?.metadata?.agent_name).accent
  const expanded = expansion.isFoldOpen(foldKey)

  const calls = tools.filter((t) => t.metadata?.kind === 'tool_call')
  const callCount = calls.length > 0 ? calls.length : tools.length
  // 孤儿结果只渲染本组的（每个 result 消息只落在一个折叠组）——用全局的孤儿列表时，
  // 任一组展开都会重复渲染全会话的孤儿。
  const orphans = pairs
    ? tools.filter(
        (t) => t.metadata?.kind === 'tool_result' && !pairs.matchedResultSeqs.has(t.seq),
      )
    : tools.filter((t) => t.metadata?.kind === 'tool_result')

  const running = !!runningSeqs && calls.some((t) => runningSeqs.has(t.seq))

  const counts: Record<string, number> = {}
  for (const t of tools) {
    if (t.metadata?.kind !== 'tool_call') continue
    const name = t.metadata?.tool_name
    if (name) counts[name] = (counts[name] ?? 0) + 1
  }
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1])
  const head = entries
    .slice(0, 4)
    .map(([n, c]) => (c > 1 ? `${n} ×${c}` : n))
    .join('、')
  const preview = entries.length > 4 ? `${head} 等` : head

  return (
    <div className="mb-2">
      <button
        type="button"
        onClick={() => expansion.toggleFold(foldKey)}
        aria-expanded={expanded}
        className={cn(
          'inline-flex max-w-full items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2.5 py-1 text-xs text-text-secondary',
          'hover:bg-surface hover:text-text',
        )}
      >
        <ChevronRight className={cn('h-3 w-3 flex-none transition-transform', expanded && 'rotate-90')} />
        {running ? <Loader2 className="h-3 w-3 flex-none animate-spin text-accent" aria-label="执行中" /> : null}
        <span className="font-semibold">{callCount} 次工具调用</span>
        {running && <span className="text-text-tertiary">执行中…</span>}
        {unconcluded && !running && <span className="text-text-tertiary">未产出结论</span>}
        {!expanded && preview && (
          <span className="max-w-[320px] overflow-hidden truncate font-mono text-text-tertiary">
            · {preview}
          </span>
        )}
      </button>
      {expanded && (
        <div className="mt-1.5 flex flex-col gap-1.5 animate-slide-down">
          {calls.map((t) => {
            const paired = pairs?.matched.get(t.seq)
            const result: ToolResultInfo | undefined = paired
              ? {
                  text: paired.metadata?.result || '',
                  durationMs: paired.metadata?.duration_ms || 0,
                  err: paired.metadata?.err || '',
                }
              : undefined
            return (
              <ToolCallCard
                key={t.seq}
                tool={t.metadata?.tool_name || ''}
                args={t.metadata?.args || ''}
                accent={accent}
                running={runningSeqs?.has(t.seq)}
                ended={ended}
                result={result}
                open={expansion.isCallOpen(t.seq)}
                onToggle={() => expansion.toggleCall(t.seq)}
              />
            )
          })}
          {orphans.map((t) => (
            <ToolResultCard
              key={t.seq}
              tool={t.metadata?.tool_name || ''}
              result={t.metadata?.result || ''}
              durationMs={t.metadata?.duration_ms || 0}
              err={t.metadata?.err || ''}
              accent={accent}
            />
          ))}
        </div>
      )}
    </div>
  )
}
