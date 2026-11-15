import { X } from 'lucide-react'
import type { Scope } from './scope'
import { useScopeLabel } from './useScopeLabel'

/**
 * 筛选栏里的范围标记：从其它页面跳来时显示「只看的是谁」（任务：目标 / 对话：标题），
 * 可一键清除回到全部。与筛选控件同高、同一圆角描边，作为筛选栏的一员而不是另起一行。
 */
export function ScopeChip({ scope, onClear }: { scope: Scope; onClear: () => void }) {
  const label = useScopeLabel(scope) ?? (scope.kind === 'task' ? '任务：…' : '对话：…')
  return (
    <span className="inline-flex h-9 max-w-full items-center gap-1.5 rounded-lg border border-border-strong bg-surface-2 pl-3 pr-1 text-sm text-text">
      <span className="min-w-0 truncate" title={label}>
        {label}
      </span>
      <button
        type="button"
        onClick={onClear}
        aria-label={`清除范围（${label}），显示全部`}
        className="inline-flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md text-text-tertiary transition-colors hover:bg-surface hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
      >
        <X aria-hidden className="h-3.5 w-3.5" />
      </button>
    </span>
  )
}
