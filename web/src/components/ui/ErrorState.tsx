import { AlertTriangle, RotateCw } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from './Button'

/**
 * 区域级失败态（列表、面板、详情加载失败）：与 EmptyState 同一骨架——图标 + 标题 + 一句原因，
 * 居中占据所在区域；能重试时给出「重试」，用户不必刷新整页。表单提交、保存这类就地失败用 InlineError。
 */
export function ErrorState({
  title = '加载失败',
  message,
  onRetry,
  retryLabel = '重试',
  className,
}: {
  title?: string
  /** 失败原因（后端中文原因经 errorText 取得） */
  message?: string
  onRetry?: () => void
  /** 按钮文案，默认「重试」；重试的动作不是重发同一次请求时写明动作（如路由错误页的「重新加载」） */
  retryLabel?: string
  className?: string
}) {
  return (
    <div role="alert" className={cn('flex flex-col items-center justify-center px-4 py-12 text-center', className)}>
      <span className="flex h-11 w-11 items-center justify-center rounded-full bg-status-error/10 text-status-error">
        <AlertTriangle aria-hidden className="h-5 w-5" />
      </span>
      <p className="mt-3 text-sm font-medium text-text">{title}</p>
      {message && <p className="mt-1 max-w-sm break-words text-xs leading-5 text-text-secondary">{message}</p>}
      {onRetry && (
        <Button variant="secondary" size="sm" className="mt-4" onClick={onRetry}>
          <RotateCw />
          {retryLabel}
        </Button>
      )}
    </div>
  )
}
