import { Check, Copy } from 'lucide-react'
import { cn } from '@/lib/utils'

interface CopyButtonProps {
  copied: boolean
  onClick: () => void
  label?: string // 未复制时文案，默认「复制」
  copiedLabel?: string // 已复制时文案，默认「已复制」
  /** 紧凑态：只显示图标（配 title/aria-label），用于元信息行内联按钮等空间受限场景。 */
  compact?: boolean
  className?: string
}

// 统一的「复制」按钮：图标 + 文案，点击后短暂切到已复制态。取代过去分散在
// FindingDrawer/LlmInvocationDrawer 里各自手写、且互不一致的复制按钮实现
// （有的用裸字符 ✓，有的用 lucide Check，视觉语言不统一）。
export function CopyButton({ copied, onClick, label = '复制', copiedLabel = '已复制', compact = false, className }: CopyButtonProps) {
  const text = copied ? copiedLabel : label
  return (
    <button
      type="button"
      onClick={onClick}
      title={compact ? text : undefined}
      aria-label={compact ? text : undefined}
      className={cn(
        'inline-flex flex-shrink-0 items-center gap-1 rounded border border-border text-[11px]',
        compact ? 'p-1' : 'px-2 py-0.5',
        copied ? 'border-accent text-accent' : 'text-muted hover:border-accent hover:text-accent',
        className,
      )}
    >
      {copied ? <Check className="h-3 w-3" aria-hidden="true" /> : <Copy className="h-3 w-3" aria-hidden="true" />}
      {!compact && text}
    </button>
  )
}
