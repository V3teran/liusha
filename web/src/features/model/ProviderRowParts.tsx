import { ChevronRight, Trash2 } from 'lucide-react'
import type { ProviderConfig } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { TYPE_LABEL, providerDisplayName } from './providerForm'

// 行头：标识键 + 协议 + 默认模型 + 状态；展开态 chevron 转下（下拉展开的隐喻）。
export function RowHeader({
  p,
  expanded,
  dirtySelected,
  onToggle,
  className,
}: {
  p: ProviderConfig
  expanded: boolean
  dirtySelected: boolean
  onToggle: () => void
  className?: string
}) {
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={expanded}
      className={cn(
        'flex min-w-0 items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-surface-2',
        expanded && 'bg-surface-2/60',
        !p.enabled && 'opacity-55',
        className,
      )}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        {!p.key_present && (
          <span
            aria-label="密钥未注入"
            title="密钥未注入，调用会失败"
            className="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-sev-high"
          />
        )}
        <span className="min-w-0 truncate text-sm font-medium text-text">
          {providerDisplayName(p)}
        </span>
        {expanded && dirtySelected && (
          <span
            aria-label="有未保存更改"
            title="有未保存更改"
            className="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-accent"
          />
        )}
      </span>
      <Badge variant="outline" className="flex-shrink-0">
        {TYPE_LABEL[p.type]}
      </Badge>
      <span className="max-w-36 flex-shrink truncate font-mono text-xs text-muted">
        {p.default_model}
      </span>
      <span className="min-w-0 flex-1 truncate font-mono text-xs text-faint">{p.base_url}</span>
      {!p.enabled && (
        <Badge variant="outline" className="flex-shrink-0">
          已停用
        </Badge>
      )}
      <ChevronRight
        className={cn(
          'h-3.5 w-3.5 flex-shrink-0 text-faint transition-transform',
          expanded && 'rotate-90',
        )}
      />
    </button>
  )
}

// 行级删除钮：垃圾桶 hover 淡显，首击变红「确认删除？」，再击才删；3 秒回退防误触。
export function RowDeleteButton({
  code,
  confirming,
  disabled,
  onArm,
  onConfirm,
}: {
  code: string
  confirming: boolean
  disabled: boolean
  onArm: () => void
  onConfirm: () => void
}) {
  return (
    <button
      type="button"
      onClick={confirming ? onConfirm : onArm}
      disabled={disabled}
      aria-label={confirming ? `确认删除 ${code}` : `删除 provider ${code}`}
      title={confirming ? '再点一次确认删除' : `删除 ${code}`}
      className={cn(
        'flex flex-shrink-0 items-center justify-center border-l border-border px-3 text-faint transition-colors hover:bg-sev-critical/10 hover:text-sev-critical',
        confirming ? 'bg-sev-critical/10 text-sev-critical' : 'opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100',
        disabled && 'cursor-default opacity-40',
      )}
    >
      {confirming ? <span className="text-xs font-medium">确认删除？</span> : <Trash2 className="h-3.5 w-3.5" />}
    </button>
  )
}
