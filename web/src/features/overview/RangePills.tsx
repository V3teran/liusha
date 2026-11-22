import { cn } from '@/lib/utils'

/** 允许的时间窗档位（与后端 AllowedWindowDays 对应）。 */
export const RANGE_DAYS = [7, 14, 30] as const

interface RangePillsProps {
  value: number
  onChange: (days: number) => void
}

/** 时间窗切换（7 / 14 / 30 天）：胶囊钮排一行，当前档位用亮底。 */
export function RangePills({ value, onChange }: RangePillsProps) {
  return (
    <div role="group" aria-label="统计时间窗" className="flex items-center gap-0.5 rounded-lg bg-surface-2 p-0.5">
      {RANGE_DAYS.map((d) => (
        <button
          key={d}
          type="button"
          aria-pressed={value === d}
          onClick={() => onChange(d)}
          className={cn(
            'rounded-md px-2 py-1 text-xs font-medium tabular-nums transition-colors',
            value === d
              ? 'bg-surface-3 text-text shadow-xs'
              : 'text-text-tertiary hover:text-text',
          )}
        >
          {d}天
        </button>
      ))}
    </div>
  )
}
