import { Loader2 } from 'lucide-react'
import { cn } from '@/lib/utils'

/**
 * 区域级加载态（列表、面板、详情、路由切换）：转圈 + 一行文字，居中占据所在区域。
 * 延迟出现（loading-reveal）：多数请求在 200ms 内返回，立刻显示只会让界面闪一下。
 */
export function LoadingState({ label = '加载中…', className }: { label?: string; className?: string }) {
  return (
    <div
      role="status"
      className={cn('loading-reveal flex items-center justify-center gap-2 px-4 py-12 text-sm text-text-secondary', className)}
    >
      <Loader2 aria-hidden className="h-4 w-4 motion-safe:animate-spin" />
      {label}
    </div>
  )
}
