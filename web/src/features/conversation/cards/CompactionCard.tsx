import { useState } from 'react'
import { Archive, ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'

interface CompactionCardProps {
  label: string // 「压缩了 N 条历史消息」（后端 Result）
  summary: string // 蒸馏摘要正文（后端 Text）
}

// 压缩卡：上下文压缩发生时显示（老 turn 蒸馏成 1 条摘要，防 context 爆）。
// 让用户对长会话的上下文裁剪有感知（对齐 Claude Code 的 compaction 可见），点击展开看蒸馏摘要。
export function CompactionCard({ label, summary }: CompactionCardProps) {
  const [open, setOpen] = useState(false)

  return (
    <div data-card="compaction" className="my-1 max-w-[70%] self-center">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={cn(
          'inline-flex w-full items-center gap-2 rounded-full border border-dashed border-border px-3.5 py-1 text-xs text-muted',
          'hover:border-accent hover:text-text',
        )}
      >
        <ChevronRight className={cn('h-3 w-3 flex-none transition-transform', open && 'rotate-90')} strokeWidth={2.5} />
        <Archive className="h-3 w-3 flex-none" strokeWidth={2} />
        <span className="flex-1 text-left">上下文已压缩{label ? ` · ${label}` : ''}</span>
        <span className="text-[11px] opacity-70">{open ? '收起' : '看摘要'}</span>
      </button>
      {open && summary && (
        <div className="mt-1.5 whitespace-pre-wrap rounded-[10px] border border-border bg-surface px-3.5 py-2.5 text-[12.5px] leading-relaxed text-text">
          {summary}
        </div>
      )}
    </div>
  )
}
