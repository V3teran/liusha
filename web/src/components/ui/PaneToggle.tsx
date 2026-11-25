import { ChevronLeft, ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'

interface PaneToggleProps {
  /** 面板现在是否展开：决定箭头方向与读屏状态 */
  open: boolean
  onToggle: () => void
  /** 按钮的名字，读屏与悬停提示同一句（如「收起侧边栏」） */
  label: string
  /** 被控制的面板的 id：读屏据此知道这个按钮管的是哪一块 */
  controls?: string
  className?: string
}

/**
 * 左侧面板的收起 / 展开开关（侧栏、对话列表共用）：28px 的图标按钮，箭头指向面板收去的方向——
 * 展开时朝左（收回去），收起后朝右（拉出来）。
 */
export function PaneToggle({ open, onToggle, label, controls, className }: PaneToggleProps) {
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-label={label}
      aria-expanded={open}
      aria-controls={controls}
      title={label}
      className={cn(
        'flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md text-text-secondary transition-colors hover:bg-surface-2 hover:text-text',
        className,
      )}
    >
      {open ? <ChevronLeft className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
    </button>
  )
}
