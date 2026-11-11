import { useState } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { CalendarClock, ChevronDown } from 'lucide-react'
import { cn } from '@/lib/utils'
import { isoToLocalInput } from '@/lib/datetime'
import { Button } from './Button'
import { FIELD_LABEL_CLASS, controlClass } from './control'

export interface DateTimeRangeValue {
  /** datetime-local 格式（YYYY-MM-DDTHH:mm），空串 = 不限 */
  start: string
  end: string
}

interface DateTimeRangeProps extends DateTimeRangeValue {
  onChange: (next: DateTimeRangeValue) => void
  /** 时间维度名（如「创建时间」）：触发钮读屏名，起止输入框标签为「<label>起 / <label>止」 */
  label: string
  className?: string
}

const MINUTE = 60_000

// 快捷区间：排障与回溯最常用的几档「最近 N」。起点 = 现在往前推，终点不限（跟随最新）。
const PRESETS: { label: string; ms: number }[] = [
  { label: '最近 15 分钟', ms: 15 * MINUTE },
  { label: '最近 1 小时', ms: 60 * MINUTE },
  { label: '最近 24 小时', ms: 24 * 60 * MINUTE },
  { label: '最近 7 天', ms: 7 * 24 * 60 * MINUTE },
]

// 原生日历按钮：默认弱化，悬停/聚焦时恢复。
const PICKER_INDICATOR =
  '[&::-webkit-calendar-picker-indicator]:cursor-pointer [&::-webkit-calendar-picker-indicator]:opacity-50 ' +
  'hover:[&::-webkit-calendar-picker-indicator]:opacity-100 focus:[&::-webkit-calendar-picker-indicator]:opacity-100'

/** YYYY-MM-DDTHH:mm → MM-DD HH:mm（筛选栏里年份是噪音：回溯窗口以天、周计）。 */
function shortStamp(local: string): string {
  return local.length >= 16 ? `${local.slice(5, 10)} ${local.slice(11, 16)}` : local
}

function summaryOf(start: string, end: string): string {
  if (!start && !end) return '全部时间'
  return `${start ? shortStamp(start) : '最早'} → ${end ? shortStamp(end) : '现在'}`
}

/**
 * 时间范围筛选：筛选栏里只占一个控件位的触发钮（摘要当前区间），展开为快捷区间 +
 * 起止两个原生 datetime-local（系统级日期选择器 + 键盘逐段输入）。
 * 此前两框常驻筛选栏，并排 ~410px，在任务 / 流量 / 审计三页把筛选栏挤成两行、
 * 手机上占三行，而时间范围是低频筛选。值的时区换算由调用方负责（见 lib/datetime）。
 */
export function DateTimeRange({ start, end, onChange, label, className }: DateTimeRangeProps) {
  const [open, setOpen] = useState(false)
  const active = Boolean(start || end)
  const summary = summaryOf(start, end)
  const input = controlClass('md', cn('w-full tabular-nums', PICKER_INDICATOR))

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button
          type="button"
          aria-label={`${label}：${summary}`}
          className={controlClass(
            'md',
            cn(
              'inline-flex cursor-pointer items-center gap-2 tabular-nums',
              active ? 'border-border-strong text-text' : 'text-text-secondary',
              className,
            ),
          )}
        >
          <CalendarClock aria-hidden className="h-4 w-4 flex-shrink-0 text-text-tertiary" />
          <span className="truncate">{summary}</span>
          <ChevronDown aria-hidden className="ml-auto h-4 w-4 flex-shrink-0 text-text-tertiary" />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          align="start"
          sideOffset={6}
          collisionPadding={16}
          aria-label={label}
          className="app-popover flex w-[min(300px,calc(100vw-32px))] flex-col gap-3 rounded-xl border border-border bg-surface p-3 shadow-lg outline-none"
        >
          <div className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL_CLASS}>快捷区间</span>
            <div className="grid grid-cols-2 gap-1.5">
              {PRESETS.map((p) => (
                <Button
                  key={p.label}
                  variant="secondary"
                  size="xs"
                  onClick={() => {
                    onChange({ start: isoToLocalInput(new Date(Date.now() - p.ms).toISOString()), end: '' })
                    setOpen(false)
                  }}
                >
                  {p.label}
                </Button>
              ))}
            </div>
          </div>
          <label className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL_CLASS}>起</span>
            <input
              type="datetime-local"
              value={start}
              max={end || undefined}
              onChange={(e) => onChange({ start: e.target.value, end })}
              aria-label={`${label}起`}
              className={input}
            />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className={FIELD_LABEL_CLASS}>止</span>
            <input
              type="datetime-local"
              value={end}
              min={start || undefined}
              onChange={(e) => onChange({ start, end: e.target.value })}
              aria-label={`${label}止`}
              className={input}
            />
          </label>
          <div className="flex items-center justify-between border-t border-border-subtle pt-3">
            <Button variant="ghost" size="xs" disabled={!active} onClick={() => onChange({ start: '', end: '' })}>
              清除
            </Button>
            <Popover.Close asChild>
              <Button size="xs">完成</Button>
            </Popover.Close>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}
