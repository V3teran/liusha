import { useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'

interface ToolResultCardProps {
  tool: string
  result: string
  durationMs: number
  err: string
  /** 左侧色条的颜色：取所属智能体的身份色（归属由所在的轮卡表明，卡内不重复标签） */
  accent?: string
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

// 工具结果卡（配不上调用的孤立结果）：状态点(成功/错误) + 工具名 + 耗时，折叠看美化结果；错误默认展开。
export function ToolResultCard({ tool, result, durationMs, err, accent }: ToolResultCardProps) {
  const [open, setOpen] = useState(!!err)
  const pretty = prettyResult(err, result)
  const previewText = preview(err, result)

  return (
    <div data-card="tool-result" data-error={!!err} className="self-start max-w-[85%]">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        style={accent ? { borderLeft: `2px solid ${accent}` } : undefined}
        className={cn(
          'flex w-full items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-left text-sm text-text',
          'hover:border-border-strong',
          err && 'border-status-error',
        )}
      >
        <ChevronRight className={cn('h-3 w-3 flex-shrink-0 text-text-secondary transition-transform', open && 'rotate-90')} />
        <span className={cn('h-1.5 w-1.5 flex-shrink-0 rounded-full', err ? 'bg-status-error' : 'bg-status-success')} />
        <code className="font-mono text-text-secondary">{tool}</code>
        <span className="text-xs text-text-secondary">{durationMs}ms</span>
        {!open && <span className="overflow-hidden truncate font-mono text-text-secondary">{previewText}</span>}
      </button>
      {open && (
        <pre tabIndex={0}
          className={cn(
            'mt-1.5 max-h-[280px] overflow-auto rounded-md border border-border bg-background p-2.5 text-xs',
            err && 'border-status-error text-status-error',
          )}
        >
          {pretty}
        </pre>
      )}
    </div>
  )
}
