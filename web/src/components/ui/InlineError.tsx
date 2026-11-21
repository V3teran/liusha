import type { ReactNode } from 'react'
import { AlertTriangle } from 'lucide-react'
import { cn } from '@/lib/utils'

/** 就地失败提示（表单提交、保存、删除失败）：紧贴触发它的操作，读屏即时播报。 */
export function InlineError({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <p role="alert" className={cn('flex items-start gap-1.5 text-xs text-status-error', className)}>
      <AlertTriangle aria-hidden className="mt-px h-3.5 w-3.5 flex-shrink-0" />
      <span className="min-w-0 break-words">{children}</span>
    </p>
  )
}
