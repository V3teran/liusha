import { useContext, type KeyboardEvent, type ReactNode } from 'react'
import { RefreshCw, Search, X } from 'lucide-react'
import { cn } from '@/lib/utils'
import { InFilterBar } from './FilterBar'

// 全站唯一的搜索框规格（单一事实源）：36px 高、圆角 12px、左放大镜（12px 处 16px 三级灰）、
// text-sm、占位符三级灰、聚焦 accent 辉光。宽度由调用方注入（作用在整组上：输入框 + 刷新钮）。
//
// 清空：位于筛选栏内时由栏的「清空筛选」统一负责；独立使用（如对话列表、模型列表）时，有内容
// 即在输入框右侧出现清空钮。同一处只有一种清空方式。
//
// 刷新钮（onRefresh）只对「词要交给服务端」的搜索框出现，是输入框外右侧的一个描边按钮（与探索图
// 工具栏的刷新钮同款）：按当前的词重新取一遍最新的数据。输入框里只留左边那一个放大镜——框内再放
// 一个放大镜按钮，两个同样的图标并排，分不清哪个是装饰哪个是操作。
// 纯本地过滤的搜索框（配置项、工具目录）词一变结果就全了，没有要刷新的东西，不传 onRefresh。
export function SearchInput({
  value,
  onChange,
  onSubmit,
  onRefresh,
  refreshing = false,
  placeholder,
  ariaLabel,
  className,
  trailing,
}: {
  value: string
  onChange: (v: string) => void
  /**
   * 回车：现在就把输入框里的词搜出去——有还没生效的词（防抖没到）立即生效，否则等同刷新。
   * 不传 = 回车不做事。
   */
  onSubmit?: () => void
  /** 按当前的词重取最新数据。不传 = 没有刷新钮 */
  onRefresh?: () => void
  /** 正在重取：刷新钮的图标转动 */
  refreshing?: boolean
  placeholder?: string
  ariaLabel?: string
  /** 布局类：宽度与外边距（如 w-72 / w-full），作用在输入框与刷新钮这一组上 */
  className?: string
  /** 渲染在输入框右侧的附件，存在时自动让出右侧内边距（独立使用时替代清空钮） */
  trailing?: ReactNode
}) {
  const inFilterBar = useContext(InFilterBar)
  const showClear = !trailing && !inFilterBar && value !== ''
  const showRefresh = !!onRefresh && !trailing

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' && onSubmit) {
      e.preventDefault() // 回车只搜，不提交外层表单
      onSubmit()
    }
  }

  return (
    <div className={cn('flex items-center gap-1.5', inFilterBar && 'max-sm:col-span-2', className)}>
      <div className="relative min-w-0 flex-1">
        <Search
          className="pointer-events-none absolute inset-y-0 left-3 my-auto h-4 w-4 text-text-tertiary"
          aria-hidden
        />
        <input
          type="text"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder={placeholder}
          aria-label={ariaLabel ?? '搜索'}
          spellCheck={false}
          className={cn(
            'h-9 w-full rounded-lg border border-border bg-surface text-sm max-sm:text-base text-text',
            'outline-none transition-colors placeholder:text-text-tertiary focus:border-accent focus:glow-accent',
            showClear ? 'pl-10 pr-8' : 'pl-10 pr-3',
          )}
        />
        {showClear && (
          <button
            type="button"
            onClick={() => onChange('')}
            aria-label="清空搜索"
            className="absolute inset-y-0 right-1.5 flex w-6 items-center justify-center rounded-md text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text"
          >
            <X className="h-4 w-4" />
          </button>
        )}
      </div>
      {showRefresh && (
        <button
          type="button"
          onClick={onRefresh}
          aria-label="刷新"
          title="按当前的词重新取最新的数据"
          disabled={refreshing}
          className={cn(
            'flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg border border-border bg-surface shadow-xs',
            'text-text-secondary transition-colors hover:border-border-strong hover:text-text active:bg-surface-2',
            'disabled:cursor-not-allowed disabled:opacity-40',
          )}
        >
          <RefreshCw aria-hidden className={cn('h-4 w-4', refreshing && 'animate-spin')} />
        </button>
      )}
      {trailing}
    </div>
  )
}
