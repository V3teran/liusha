import { useState } from 'react'
import { ChevronRight, Loader2 } from 'lucide-react'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { cn } from '@/lib/utils'

interface ToolCallCardProps {
  tool: string
  args: string
  agentName?: string
  running?: boolean // 执行中：已发 tool_call 未见配对 tool_result（任务活跃时）
}

function prettyArgs(args: string): string {
  if (!args) return ''
  try {
    return JSON.stringify(JSON.parse(args), null, 2)
  } catch {
    return args
  }
}

// 工具调用卡：折叠头（工具名）+ 展开看美化后的入参 JSON。按 agent 名着左边框 + chip 色，区分谁在调用。
// 执行中以 spinner 替换状态点——长工具（浏览器/命令）进行中一眼可辨。
export function ToolCallCard({ tool, args, agentName, running }: ToolCallCardProps) {
  const [open, setOpen] = useState(false)
  const pretty = prettyArgs(args)
  const hasArgs = !!pretty && pretty !== '{}'
  const accent = agentAccent(agentName) // 每个 agent 独立色

  return (
    <div data-card="tool-call" data-running={!!running} className="self-start max-w-[85%]">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        style={agentName ? { borderLeft: `2px solid ${accent.accent}` } : undefined}
        className={cn(
          'inline-flex items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-[13px] text-text',
          'hover:border-accent',
        )}
      >
        <ChevronRight className={cn('h-3 w-3 text-muted transition-transform', open && 'rotate-90')} strokeWidth={2.5} />
        {running ? (
          <Loader2 className="h-3 w-3 animate-spin text-accent" aria-label="执行中" />
        ) : (
          <span className="h-1.5 w-1.5 rounded-full bg-accent" />
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
      </button>
      {open && hasArgs && (
        <pre className="mt-1.5 max-h-[280px] overflow-auto rounded-[6px] border border-border bg-background p-2.5 text-xs">
          {pretty}
        </pre>
      )}
    </div>
  )
}
