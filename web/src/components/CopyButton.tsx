import { Check, Copy } from 'lucide-react'
import { cn } from '@/lib/utils'

interface CopyButtonProps {
  copied: boolean
  onClick: () => void
  size?: 'sm' | 'md'
  className?: string
}

/**
 * CopyButton - 复制按钮组件
 *
 * 设计改进：
 * 1. 支持不同尺寸
 * 2. 更清晰的状态反馈（图标切换 + 颜色变化）
 * 3. 更流畅的过渡动画
 */
export function CopyButton({ copied, onClick, size = 'md', className }: CopyButtonProps) {
  const sizeClasses = {
    sm: 'h-7 w-7 p-1.5',
    md: 'h-8 w-8 p-2',
  }

  const iconSizes = {
    sm: 'h-3.5 w-3.5',
    md: 'h-4 w-4',
  }

  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={copied ? '已复制' : '复制'}
      className={cn(
        'inline-flex items-center justify-center rounded-md',
        'text-text-tertiary transition-all duration-200',
        'hover:bg-surface-2 hover:text-accent',
        copied && 'text-status-success',
        sizeClasses[size],
        className
      )}
    >
      {copied ? (
        <Check className={cn(iconSizes[size], 'animate-scale-in')} strokeWidth={2.5} />
      ) : (
        <Copy className={iconSizes[size]} strokeWidth={2} />
      )}
    </button>
  )
}
