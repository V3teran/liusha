import { useState } from 'react'
import { ChevronRight, Loader2 } from 'lucide-react'
import type { Message } from '@/api/types'
import { cn } from '@/lib/utils'
import { ToolCallCard, type ToolResultInfo } from './cards/ToolCallCard'
import { ToolResultCard } from './cards/ToolResultCard'

interface StepToolsProps {
  tools: Message[]
  runningSeqs?: Set<number>
  pairs?: { matched: Map<number, Message>; matchedResultSeqs: Set<number> }
  /** 任务已终态：悬挂 call（有 call 无 result）按「已中断」呈现而非脉冲点。 */
  ended?: boolean
}

export function StepTools({ tools, runningSeqs, pairs, ended }: StepToolsProps) {
  const [expanded, setExpanded] = useState(false)

  const calls = tools.filter((t) => t.metadata?.kind === 'tool_call')
  const callCount = calls.length > 0 ? calls.length : tools.length
  // 孤儿结果只渲染本组区间内的（每个 result 消息只落在一个折叠组）——此前
  // 直接用全局 orphanResults，任一组展开都会重复渲染全会话的孤儿。
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
    <div className="ml-9 max-w-3xl pb-5">
      <button
        type="button"
        onClick={() => setExpanded((e) => !e)}
        className={cn(
          'inline-flex items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2.5 py-1 text-xs text-muted',
          'hover:bg-surface hover:text-text',
        )}
      >
        <ChevronRight className={cn('h-3 w-3 flex-none transition-transform', expanded && 'rotate-90')} strokeWidth={2.5} />
        {running ? <Loader2 className="h-3 w-3 flex-none animate-spin text-accent" aria-label="执行中" /> : null}
        <span className="font-semibold">{callCount} 次工具调用</span>
        {running && <span className="text-faint">执行中…</span>}
        {!expanded && preview && (
          <span className="ml-0.5 max-w-[320px] overflow-hidden truncate font-mono opacity-70">
            · {preview}
          </span>
        )}
      </button>
      {expanded && (
        <div className="mt-1.5 flex flex-col gap-1.5">
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
                agentName={t.metadata?.agent_name}
                running={runningSeqs?.has(t.seq)}
                ended={ended}
                result={result}
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
              agentName={t.metadata?.agent_name}
            />
          ))}
        </div>
      )}
    </div>
  )
}
