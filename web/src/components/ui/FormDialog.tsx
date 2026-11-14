import * as Dialog from '@radix-ui/react-dialog'
import { AlertTriangle, X } from 'lucide-react'
import type { FormEvent, ReactNode } from 'react'
import { Button } from '@/components/ui/Button'

interface FormDialogProps {
  open: boolean
  /** Radix 受控开关：false = 取消（Esc / 点遮罩 / 点取消或关闭按钮）。提交中不可关闭。 */
  onOpenChange: (open: boolean) => void
  title: string
  /** 标题下的说明：提交会产生什么后果。 */
  description?: ReactNode
  /** 提交按钮文案，动词要具体（「确认否决」而非「确定」）。 */
  submitLabel: string
  /** 危险操作（不可逆）：提交钮用 danger 变体、标题带警示图标。 */
  danger?: boolean
  /** 表单内容是否可提交（必填项齐全）。 */
  canSubmit: boolean
  /** 提交进行中：提交钮禁用并显示进行态，防重复提交。 */
  pending: boolean
  /** 提交失败的原因（后端中文原因，见 errorText）。 */
  error?: string
  onSubmit: () => void
  children: ReactNode
}

// 表单弹层：标题 + 说明 + 字段 + 提交 / 取消。与 ConfirmDialog 同一层级规格（app-overlay +
// tac-modal），区别在于它承载输入；⌘/Ctrl+Enter 在任一字段内即提交（多行输入框里 Enter
// 是换行）。取消摆在提交前，首个可聚焦元素落在字段上。
export function FormDialog({
  open,
  onOpenChange,
  title,
  description,
  submitLabel,
  danger,
  canSubmit,
  pending,
  error,
  onSubmit,
  children,
}: FormDialogProps) {
  const submit = (e?: FormEvent) => {
    e?.preventDefault()
    if (canSubmit && !pending) onSubmit()
  }
  return (
    <Dialog.Root open={open} onOpenChange={(next) => !pending && onOpenChange(next)}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content className="tac-modal fixed left-1/2 top-1/2 w-[520px] max-w-[calc(100vw-32px)] -translate-x-1/2 -translate-y-1/2 rounded-xl border border-border bg-surface shadow-lg outline-none">
          <form
            onSubmit={submit}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) submit(e)
            }}
          >
            <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
              <Dialog.Title className="flex items-center gap-2 text-sm font-semibold text-text">
                {danger && <AlertTriangle className="h-4 w-4 text-sev-critical" aria-hidden />}
                {title}
              </Dialog.Title>
              <Dialog.Close asChild>
                <button
                  type="button"
                  aria-label="关闭"
                  disabled={pending}
                  className="rounded-md p-1 text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text disabled:opacity-50"
                >
                  <X className="h-4 w-4" />
                </button>
              </Dialog.Close>
            </div>
            <div className="flex flex-col gap-4 px-5 py-4">
              {description ? (
                <Dialog.Description className="text-[12.5px] leading-relaxed text-text-secondary">
                  {description}
                </Dialog.Description>
              ) : (
                <Dialog.Description className="sr-only">{title}</Dialog.Description>
              )}
              {children}
              {error && (
                <p role="alert" className="text-xs text-sev-critical">
                  ⚠ {error}
                </p>
              )}
            </div>
            <div className="flex items-center justify-end gap-2.5 border-t border-border bg-surface-2/50 px-5 py-3.5">
              <span className="mr-auto text-[11px] text-text-tertiary">⌘/Ctrl+Enter 提交</span>
              <Button type="button" variant="secondary" disabled={pending} onClick={() => onOpenChange(false)}>
                取消
              </Button>
              <Button type="submit" variant={danger ? 'danger' : 'primary'} disabled={!canSubmit || pending}>
                {pending ? '提交中…' : submitLabel}
              </Button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
