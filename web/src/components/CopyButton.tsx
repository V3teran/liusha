import { Check, Copy } from 'lucide-react'
import { cn } from '@/lib/utils'

interface CopyButtonProps {
  copied: boolean
  onClick: () => void
  label?: string // tooltip/读屏文案，默认「复制」
  copiedLabel?: string // 已复制态 tooltip，默认「已复制」
  className?: string
}

// 统一的「复制」按钮：全站唯一图标形态（16px + title 提示）——业界消息操作
// 惯例（hover 可辨、不占空间、无文字冗余）。淡色常显 → hover 强调色 + 底色；
// 点击后短暂切到已复制态（Check + 强调色）。label 仅作 tooltip，不渲染文字。
export function CopyButton({ copied, onClick, label = '复制', copiedLabel = '已复制', className }: CopyButtonProps) {
  const text = copied ? copiedLabel : label
  return (
    <button
      type="button"
      onClick={onClick}
      title={text}
      aria-label={text}
      className={cn(
        'inline-flex flex-shrink-0 items-center justify-center rounded-md p-1.5 transition-colors',
        copied ? 'text-accent' : 'text-faint hover:bg-surface-2 hover:text-accent',
        className,
      )}
    >
      {copied ? (
        <Check className="h-4 w-4" aria-hidden="true" />
      ) : (
        <Copy className="h-4 w-4" aria-hidden="true" />
      )}
    </button>
  )
}
