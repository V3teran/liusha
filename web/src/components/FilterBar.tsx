import type { ReactNode } from 'react'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'

interface FilterBarProps {
  /** 筛选控件（全站顺序：枚举下拉 粗→细 → 关键词搜索 → 时间范围 → 布尔开关） */
  children: ReactNode
  /** 右侧附属区：分页器 / 计数等，始终钉在首行右端，不随筛选控件换行 */
  trailing?: ReactNode
  /** 是否存在生效中的筛选（决定「清空筛选」是否出现） */
  canReset?: boolean
  onReset?: () => void
  /** 地标名（读屏「搜索」区域），缺省「筛选」 */
  label?: string
  className?: string
}

/**
 * 列表页筛选栏（全站唯一形态）：位于页头与内容滚动区之间、不随内容滚动。
 *
 * 布局：左侧筛选控件组可换行；右侧附属区固定在首行右端——此前各页把分页塞进
 * 同一个 flex-wrap 行，控件一多分页就被挤到第二行，各页落点不一。
 * 窄屏（< sm）上下堆叠：附属区落到控件组下方。
 */
export function FilterBar({ children, trailing, canReset, onReset, label = '筛选', className }: FilterBarProps) {
  return (
    <div
      role="search"
      aria-label={label}
      className={cn(
        'flex flex-shrink-0 flex-col gap-3 border-b border-border-subtle bg-surface/50 px-6 py-3 sm:flex-row sm:items-start',
        className,
      )}
    >
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2.5">
        {children}
        {canReset && onReset && (
          <button
            type="button"
            onClick={onReset}
            className="inline-flex h-9 items-center gap-1 rounded-lg px-2.5 text-sm text-text-secondary transition-colors hover:bg-surface-2 hover:text-text"
          >
            <X aria-hidden className="h-3.5 w-3.5" />
            清空筛选
          </button>
        )}
      </div>
      {trailing && <div className="flex h-9 flex-shrink-0 items-center gap-3">{trailing}</div>}
    </div>
  )
}
