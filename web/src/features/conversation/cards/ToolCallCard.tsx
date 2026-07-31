import { useState } from 'react'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { cn } from '@/lib/utils'

interface ToolCallCardProps {
  tool: string
  args: string
  agentName?: string
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
export function ToolCallCard({ tool, args, agentName }: ToolCallCardProps) {
  const [open, setOpen] = useState(false)
  const pretty = prettyArgs(args)
  const hasArgs = !!pretty && pretty !== '{}'
  const accent = agentAccent(agentName) // 每个 agent 独立色

  return (
    <div data-card="tool-call" className="self-start max-w-[85%]">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        style={agentName ? { borderLeft: `2px solid ${accent.accent}` } : undefined}
        className={cn(
          'inline-flex items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-[13px] text-text',
          'hover:border-accent',
        )}
      >
        <span className={cn('text-[11px] text-muted transition-transform', open && 'rotate-90')}>▸</span>
        <span className="h-1.5 w-1.5 rounded-full bg-accent" />
        <span className="text-muted">调用</span>
        <code className="font-mono font-semibold text-accent">{tool}</code>
        {agentName && (
          <span
            className="rounded px-1.5 py-0 font-mono text-[10.5px]"
            style={{ color: accent.accent, background: accent.soft }}
          >
            {agentLabel(agentName)}
          </span>
        )}
      </button>
      {open && hasArgs && (
        <pre className="mt-1.5 max-h-[280px] overflow-auto rounded-[6px] border border-border bg-background p-2.5 text-xs">
          {pretty}
        </pre>
      )}
    </div>
  )
}
