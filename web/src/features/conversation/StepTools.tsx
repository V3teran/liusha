import { useState } from 'react'
import { ChevronRight } from 'lucide-react'
import type { Message } from '@/api/types'
import { classifyMessage } from '@/lib/messageKind'
import { cn } from '@/lib/utils'
import { ToolCallCard } from './cards/ToolCallCard'
import { ToolResultCard } from './cards/ToolResultCard'

interface StepToolsProps {
  tools: Message[]
}

// 步内工具折叠：一个 reasoning(想) 之下的若干工具调用默认收起成一行按钮，点击展开。
// 减少会话流噪音——工具调用细节按需查看；派发/漏洞/想 不进这里（由 TimelineThread 留在导轨上）。
export function StepTools({ tools }: StepToolsProps) {
  const [expanded, setExpanded] = useState(false)

  // 工具调用次数：一次调用产生 tool_call + tool_result 两条消息，计数只数 tool_call（发起）——
  // 否则一来一回会翻倍（38 次显示成 76）。全是孤立 result（call 丢失）时回退总条数兜底。
  const calls = tools.filter((t) => t.Metadata?.Kind === 'tool_call').length
  const callCount = calls > 0 ? calls : tools.length

  // 工具预览：按类别聚合「次数」——计数用「次」(callCount 总次数)反映工作量，预览用
  // 「类别 ×次数」(run_command ×5、browser_use ×2)反映干了啥+各几次。只数 tool_call(发起)，不重复算结果。
  const counts: Record<string, number> = {}
  for (const t of tools) {
    if (t.Metadata?.Kind !== 'tool_call') continue
    const name = t.Metadata?.ToolName
    if (name) counts[name] = (counts[name] ?? 0) + 1
  }
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1])
  const head = entries
    .slice(0, 4)
    .map(([n, c]) => (c > 1 ? `${n} ×${c}` : n))
    .join('、')
  const preview = entries.length > 4 ? `${head} 等` : head

  return (
    <div className="ml-10 self-start max-w-[88%]">
      <button
        type="button"
        onClick={() => setExpanded((e) => !e)}
        className={cn(
          'inline-flex items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2.5 py-1 text-[11.5px] text-muted',
          'hover:bg-surface hover:text-text',
        )}
      >
        <ChevronRight className={cn('h-3 w-3 flex-none transition-transform', expanded && 'rotate-90')} strokeWidth={2.5} />
        <span className="font-semibold">{callCount} 次工具调用</span>
        {!expanded && preview && (
          <span className="ml-0.5 max-w-[320px] overflow-hidden truncate font-mono opacity-70">
            · {preview}
          </span>
        )}
      </button>
      {expanded && (
        <div className="mt-1.5 flex flex-col gap-1.5">
          {tools.map((t) => {
            const tag = classifyMessage(t)
            if (tag === 'tool-call') {
              return (
                <ToolCallCard key={t.Seq} tool={t.Metadata?.ToolName || ''} args={t.Metadata?.Args || ''} agentName={t.Metadata?.AgentName} />
              )
            }
            if (tag === 'tool-result') {
              return (
                <ToolResultCard
                  key={t.Seq}
                  tool={t.Metadata?.ToolName || ''}
                  result={t.Metadata?.Result || ''}
                  durationMs={t.Metadata?.DurationMs || 0}
                  err={t.Metadata?.Err || ''}
                  agentName={t.Metadata?.AgentName}
                />
              )
            }
            return null
          })}
        </div>
      )}
    </div>
  )
}
