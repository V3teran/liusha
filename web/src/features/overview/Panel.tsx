import { useId, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ArrowRight } from 'lucide-react'
import { cn } from '@/lib/utils'

/**
 * 概览页的一个区块：标题（可带一个简短的量，如条数）+ 右上角去处 + 内容。标题即区块的可访问名。
 * 只用细边框分层、不加阴影：层级交给关键数字的字号，区块之间不争视线。
 */
export function Panel({
  title,
  meta,
  action,
  children,
  className,
}: {
  title: string
  meta?: string
  action?: ReactNode
  children: ReactNode
  className?: string
}) {
  const id = useId()
  return (
    <section
      aria-labelledby={id}
      className={cn('flex min-w-0 flex-col rounded-xl border border-border bg-surface', className)}
    >
      <header className="flex items-center justify-between gap-3 border-b border-border-subtle px-4 py-3 sm:px-5">
        {/* 量放在标题之外：区块的可访问名只是标题，不随数字变化。标题与去处不折行，位置不够时
            先截断量 */}
        <div className="flex min-w-0 items-baseline gap-2">
          <h2 id={id} className="whitespace-nowrap text-sm font-semibold text-text">
            {title}
          </h2>
          {meta && <span className="min-w-0 truncate text-xs tabular-nums text-text-tertiary">{meta}</span>}
        </div>
        {action && <div className="flex-shrink-0 whitespace-nowrap">{action}</div>}
      </header>
      <div className="flex min-h-0 flex-1 flex-col px-4 py-3 sm:px-5">{children}</div>
    </section>
  )
}

/** 区块右上角的去处：到承载完整数据的页面。 */
export function PanelLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link
      to={to}
      className="inline-flex items-center gap-1 rounded text-xs text-text-secondary transition-colors hover:text-accent"
    >
      {children}
      <ArrowRight aria-hidden className="h-3 w-3" />
    </Link>
  )
}

/** 区块内的空态：一行说明，不占大块面积（概览页上空着的区块不该比有数据的更显眼）。 */
export function PanelEmpty({ children }: { children: ReactNode }) {
  return <p className="flex flex-1 items-center justify-center py-6 text-center text-xs text-text-secondary">{children}</p>
}
