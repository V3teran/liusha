import { useState } from 'react'
import { ChevronRight, Loader2 } from 'lucide-react'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { cn } from '@/lib/utils'

/** 配对的执行结果（一动作一卡：调用与观测同一张卡呈现）。 */
export interface ToolResultInfo {
  text: string
  durationMs: number
  err: string
}

interface ToolCallCardProps {
  tool: string
  args: string
  agentName?: string
  running?: boolean // 执行中：已发 tool_call 未见配对 tool_result（任务活跃时）
  result?: ToolResultInfo // 已配对的结果；与 running 互斥
}

function prettyArgs(args: string): string {
  if (!args) return ''
  try {
    return JSON.stringify(JSON.parse(args), null, 2)
  } catch {
    return args
  }
}

function resultPreview(r: ToolResultInfo): string {
  const s = (r.err || r.text || '').replace(/\s+/g, ' ').trim()
  return s.length > 64 ? s.slice(0, 64) + '…' : s
}

// 工具动作卡：一动作一卡——调用（工具名/入参）与观测（输出/耗时/错误）同卡
// 呈现，折叠展开看详情。按 agent 名着左边框 + chip 色；执行中 spinner、
// 失败红点红边、成功绿点。孤儿结果（call 丢失）由 ToolResultCard 兜底展示。
export function ToolCallCard({ tool, args, agentName, running, result }: ToolCallCardProps) {
  const [open, setOpen] = useState(false)
  const pretty = prettyArgs(args)
  const hasArgs = !!pretty && pretty !== '{}'
  const accent = agentAccent(agentName) // 每个 agent 独立色
  const failed = !!result?.err

  return (
    <div data-card="tool-call" data-running={!!running} data-error={failed} className="self-start max-w-[85%]">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        style={agentName ? { borderLeft: `2px solid ${accent.accent}` } : undefined}
        className={cn(
          'inline-flex items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-[13px] text-text',
          'hover:border-accent',
          failed && 'border-sev-critical',
        )}
      >
        <ChevronRight className={cn('h-3 w-3 text-muted transition-transform', open && 'rotate-90')} strokeWidth={2.5} />
        {running ? (
          <Loader2 className="h-3 w-3 animate-spin text-accent" aria-label="执行中" />
        ) : (
          <span className={cn('h-1.5 w-1.5 rounded-full', failed ? 'bg-sev-critical' : result ? 'bg-emerald-500' : 'bg-accent')} />
        )}
        <span className="text-muted">调用</span>
        <code className="font-mono font-semibold text-accent">{tool}</code>
        {agentName && (
          <span
            className="rounded-md px-1.5 py-0 font-mono text-[10.5px]"
            style={{ color: accent.accent, background: accent.soft }}
          >
            {agentLabel(agentName)}
          </span>
        )}
        {running && <span className="text-[11px] text-faint">执行中…</span>}
        {!running && result && <span className="text-[11px] text-muted">{result.durationMs}ms</span>}
        {!open && result && <span className="overflow-hidden truncate font-mono text-muted">{resultPreview(result)}</span>}
      </button>
      {open && (
        <div className="mt-1.5 flex max-w-full flex-col gap-1.5">
          {hasArgs && (
            <pre className="max-h-[280px] overflow-auto rounded-[6px] border border-border bg-background p-2.5 text-xs">
              {pretty}
            </pre>
          )}
          {result && (
            <pre
              className={cn(
                'max-h-[280px] overflow-auto rounded-[6px] border border-border bg-background p-2.5 text-xs',
                failed && 'border-sev-critical text-sev-critical',
              )}
            >
              {result.err || result.text}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}
