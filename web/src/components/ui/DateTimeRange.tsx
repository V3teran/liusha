import { ArrowRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { controlClass } from './control'

export interface DateTimeRangeValue {
  /** datetime-local 格式（YYYY-MM-DDTHH:mm），空串 = 不限 */
  start: string
  end: string
}

interface DateTimeRangeProps extends DateTimeRangeValue {
  onChange: (next: DateTimeRangeValue) => void
  startLabel: string
  endLabel: string
  className?: string
}

// 原生日历按钮：默认弱化，悬停/聚焦时恢复——避免两枚日历图标在筛选栏里抢视觉。
const PICKER_INDICATOR =
  '[&::-webkit-calendar-picker-indicator]:cursor-pointer [&::-webkit-calendar-picker-indicator]:opacity-50 ' +
  'hover:[&::-webkit-calendar-picker-indicator]:opacity-100 focus:[&::-webkit-calendar-picker-indicator]:opacity-100'

/**
 * 时间范围：起止两个原生 datetime-local（系统级日期选择器 + 键盘逐段输入），
 * 中间箭头示意区间方向。值的时区换算由调用方负责（见 lib/datetime）。
 */
export function DateTimeRange({ start, end, onChange, startLabel, endLabel, className }: DateTimeRangeProps) {
  const input = controlClass('md', cn('w-full tabular-nums sm:w-48', PICKER_INDICATOR))
  // 窄屏（< sm）两框纵向堆叠、撑满宽度（并排需 ~410px，超出手机可用宽度）；sm 起横排。
  return (
    <div className={cn('flex w-full flex-col gap-1.5 sm:inline-flex sm:w-auto sm:flex-row sm:items-center', className)}>
      <input
        type="datetime-local"
        value={start}
        onChange={(e) => onChange({ start: e.target.value, end })}
        aria-label={startLabel}
        className={input}
      />
      <ArrowRight aria-hidden className="h-3.5 w-3.5 flex-shrink-0 rotate-90 self-center text-text-tertiary sm:rotate-0" />
      <input
        type="datetime-local"
        value={end}
        onChange={(e) => onChange({ start, end: e.target.value })}
        aria-label={endLabel}
        className={input}
      />
    </div>
  )
}
