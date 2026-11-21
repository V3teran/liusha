import { useContext, type ReactNode } from 'react'
import { Search, X } from 'lucide-react'
import { cn } from '@/lib/utils'
import { InFilterBar } from './FilterBar'

// 全站唯一的搜索框规格（单一事实源）：36px 高、圆角 12px、左放大镜（12px 处 16px 三级灰）、
// text-sm、占位符三级灰、聚焦 accent 辉光。宽度由调用方注入。
//
// 清空：位于筛选栏内时由栏的「清空筛选」统一负责；独立使用（如对话列表、模型列表）时，有内容
// 即在右侧出现清空钮。同一处只有一种清空方式。
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
  /** 渲染在输入框右侧的附件，存在时自动让出右侧内边距（独立使用时替代清空钮） */
  trailing?: ReactNode
}) {
  const inFilterBar = useContext(InFilterBar)
  const clear =
    !trailing && !inFilterBar && value !== '' ? (
      <button
        type="button"
        onClick={() => onChange('')}
        aria-label="清空搜索"
        className="flex text-text-tertiary transition-colors hover:text-text"
      >
        <X className="h-4 w-4" />
      </button>
    ) : null
  const right = trailing ?? clear
  return (
    <div className={cn('relative', inFilterBar && 'max-sm:col-span-2', className)}>
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
          'h-9 w-full rounded-lg border border-border bg-surface text-sm max-sm:text-base text-text',
          'outline-none transition-colors placeholder:text-text-tertiary focus:border-accent focus:glow-accent',
          right ? 'pl-10 pr-10' : 'pl-10 pr-3',
        )}
      />
      {right && <div className="absolute right-3 top-1/2 flex -translate-y-1/2">{right}</div>}
    </div>
  )
}
