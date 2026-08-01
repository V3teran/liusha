import { useState } from 'react'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { cn } from '@/lib/utils'

interface ToolResultCardProps {
  tool: string
  result: string
  durationMs: number
  err: string
  agentName?: string
}

function prettyResult(err: string, result: string): string {
  const raw = err || result
  if (!raw) return ''
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

function preview(err: string, result: string): string {
  const s = (err || result || '').replace(/\s+/g, ' ').trim()
  return s.length > 64 ? s.slice(0, 64) + '…' : s
}

// 工具结果卡：状态点(成功/错误) + 工具名 + 耗时，折叠看美化结果；错误默认展开。按 agent 名着色。
export function ToolResultCard({ tool, result, durationMs, err, agentName }: ToolResultCardProps) {
  const [open, setOpen] = useState(!!err)
  const accent = agentAccent(agentName) // 每个 agent 独立色
  const pretty = prettyResult(err, result)
  const previewText = preview(err, result)

  return (
    <div data-card="tool-result" data-error={!!err} className="self-start max-w-[85%]">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        style={agentName ? { borderLeft: `2px solid ${accent.accent}` } : undefined}
        className={cn(
          'flex w-full items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-left text-[12.5px] text-text',
          'hover:border-border-strong',
          err && 'border-sev-critical',
        )}
      >
        <span className={cn('text-[11px] text-muted transition-transform', open && 'rotate-90')}>▸</span>
        <span className={cn('h-1.5 w-1.5 flex-shrink-0 rounded-full', err ? 'bg-sev-critical' : 'bg-emerald-500')} />
        <code className="font-mono text-muted">{tool}</code>
        <span className="text-[11px] text-muted">{durationMs}ms</span>
        {agentName && (
          <span
            className="flex-shrink-0 rounded px-1.5 py-0 font-mono text-[10.5px]"
            style={{ color: accent.accent, background: accent.soft }}
          >
            {agentLabel(agentName)}
          </span>
        )}
        {!open && <span className="overflow-hidden truncate font-mono text-muted">{previewText}</span>}
      </button>
      {open && (
        <pre
          className={cn(
            'mt-1.5 max-h-[280px] overflow-auto rounded-[6px] border border-border bg-background p-2.5 text-xs',
            err && 'border-sev-critical text-sev-critical',
          )}
        >
          {pretty}
        </pre>
      )}
    </div>
  )
}
