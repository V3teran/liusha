import { Search } from 'lucide-react'
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

// 全站唯一的搜索框规格（单一事实源）：36px 高、圆角 12px、左放大镜（12px 处
// 16px 三级灰）、text-sm、占位符三级灰、聚焦 accent 辉光。宽度由调用方注入
// （w-72 页面主过滤 / w-full 容器内自适应）；trailing 可挂右侧附件（如清空钮）。
export function SearchInput({
  value,
  onChange,
  placeholder,
  ariaLabel,
  className,
  trailing,
}: {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  ariaLabel?: string
  /** 布局类：宽度与外边距（如 w-72 / w-full / -mx-6） */
  className?: string
  /** 渲染在输入框右侧的附件（如清空按钮），存在时自动让出右侧内边距 */
  trailing?: ReactNode
}) {
  return (
    <div className={cn('relative', className)}>
      <Search
        className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary"
        aria-hidden
      />
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        aria-label={ariaLabel ?? '搜索'}
        spellCheck={false}
        className={cn(
          'h-9 w-full rounded-lg border border-border bg-surface text-sm text-text',
          'outline-none transition-colors placeholder:text-text-tertiary focus:border-accent focus:glow-accent',
          trailing ? 'pl-10 pr-10' : 'pl-10 pr-3',
        )}
      />
      {trailing && (
        <div className="absolute right-3 top-1/2 -translate-y-1/2">{trailing}</div>
      )}
    </div>
  )
}
