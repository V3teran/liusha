import { useState } from 'react'
import { ChevronRight, Loader2 } from 'lucide-react'
import type { Message } from '@/api/types'
import { cn } from '@/lib/utils'
import { ToolCallCard, type ToolResultInfo } from './cards/ToolCallCard'
import { ToolResultCard } from './cards/ToolResultCard'

interface StepToolsProps {
  tools: Message[]
  /** 执行中的 tool_call Seq 集合（任务活跃时计算）；组内任一执行中 → 组头 spinner。 */
  runningSeqs?: Set<number>
  /** 调用→结果配对（一动作一卡）；缺省时不配对（历史兼容渲染）。 */
  pairs?: { matched: Map<number, Message>; orphanResults: Message[] }
}

// 步内工具折叠（方案 B）：一次推理之下的若干工具动作聚成独立一行，缩进对齐卡片正文（ml-9），
// 点击展开。工具动作不是推理，故独立成行而非折进推理卡；默认收起减少噪音。
// 一动作一卡：call 与配对 result 同卡呈现（业界动作卡范式）；组头 spinner 在默认
// 折叠态也可见执行进展；孤儿结果（call 丢失）以独立结果卡兜底。
export function StepTools({ tools, runningSeqs, pairs }: StepToolsProps) {
  const [expanded, setExpanded] = useState(false)

  const calls = tools.filter((t) => t.Metadata?.kind === 'tool_call')
  const callCount = calls.length > 0 ? calls.length : tools.length
  const orphans = pairs?.orphanResults ?? tools.filter((t) => t.Metadata?.kind === 'tool_result')

  const running = !!runningSeqs && calls.some((t) => runningSeqs.has(t.Seq))

  // 工具预览：按类别聚合「次数」——计数用「次」(callCount 总次数)反映工作量，预览用
  // 「类别 ×次数」(run_command ×5、drive_browser ×2)反映干了啥+各几次。
  const counts: Record<string, number> = {}
  for (const t of tools) {
    if (t.Metadata?.kind !== 'tool_call') continue
    const name = t.Metadata?.tool_name
    if (name) counts[name] = (counts[name] ?? 0) + 1
  }
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1])
  const head = entries
    .slice(0, 4)
    .map(([n, c]) => (c > 1 ? `${n} ×${c}` : n))
    .join('、')
  const preview = entries.length > 4 ? `${head} 等` : head

  return (
    <div className="ml-9 max-w-[85%] pb-[18px]">
      <button
        type="button"
        onClick={() => setExpanded((e) => !e)}
        className={cn(
          'inline-flex items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2.5 py-1 text-[11.5px] text-muted',
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
            const paired = pairs?.matched.get(t.Seq)
            const result: ToolResultInfo | undefined = paired
              ? {
                  text: paired.Metadata?.result || '',
                  durationMs: paired.Metadata?.duration_ms || 0,
                  err: paired.Metadata?.err || '',
                }
              : undefined
            return (
              <ToolCallCard
                key={t.Seq}
                tool={t.Metadata?.tool_name || ''}
                args={t.Metadata?.args || ''}
                agentName={t.Metadata?.agent_name}
                running={runningSeqs?.has(t.Seq)}
                result={result}
              />
            )
          })}
          {orphans.map((t) => (
            <ToolResultCard
              key={t.Seq}
              tool={t.Metadata?.tool_name || ''}
              result={t.Metadata?.result || ''}
              durationMs={t.Metadata?.duration_ms || 0}
              err={t.Metadata?.err || ''}
              agentName={t.Metadata?.agent_name}
            />
          ))}
        </div>
      )}
    </div>
  )
}
