import type { ButtonHTMLAttributes } from 'react'
import { Check, Loader2, Plus } from 'lucide-react'
import { cn } from '@/lib/utils'

interface ToggleChipProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'type'> {
  /** 是否处于选中（已装配 / 已归入）态，同步为 aria-pressed */
  pressed: boolean
  /** 写入中：显示转圈并禁用 */
  busy?: boolean
  /** 标识符类内容（工具名等）用等宽小号字 */
  mono?: boolean
}

/**
 * 可切换胶囊：多选集合里「点一下装 / 卸」的成员（智能体装配、工具名单、档位归入）。
 * 选中态不只靠颜色：强调描边 + 淡底 + ✓；未选中带 +，图标同时说明点击后会发生什么。
 */
export function ToggleChip({ pressed, busy = false, mono = false, disabled, className, children, ...rest }: ToggleChipProps) {
  const Icon = busy ? Loader2 : pressed ? Check : Plus
  return (
    <button
      type="button"
      aria-pressed={pressed}
      aria-busy={busy || undefined}
      disabled={disabled || busy}
      className={cn(
        'inline-flex h-7 cursor-pointer items-center gap-1 rounded-md border px-2 transition-colors duration-150',
        // 选中且不可再点（单选归属：已在此档）是确定态而非不可用态，不降透明度。
        pressed ? 'disabled:cursor-default' : 'disabled:cursor-not-allowed disabled:opacity-60',
        mono ? 'font-mono text-xs' : 'text-sm',
        pressed
          ? 'border-accent bg-accent-soft font-medium text-text'
          : 'border-border bg-surface text-text-secondary hover:border-border-strong hover:text-text',
        className,
      )}
      {...rest}
    >
      <Icon
        aria-hidden
        className={cn('h-3.5 w-3.5 flex-shrink-0', busy && 'animate-spin', !pressed && !busy && 'text-text-tertiary')}
      />
      {children}
    </button>
  )
}
