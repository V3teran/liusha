import * as Dialog from '@radix-ui/react-dialog'
import { AlertTriangle } from 'lucide-react'
import type { ReactNode } from 'react'
import { Button } from '@/components/ui/Button'

interface ConfirmDialogProps {
  open: boolean
  /** Radix 受控开关：false = 取消（Esc / 点遮罩 / 点取消按钮都走这里）。 */
  onOpenChange: (open: boolean) => void
  title: string
  message: ReactNode
  /** 确认按钮文案，动词要具体（「确认停止」而非「确定」），与触发动作同名。 */
  confirmLabel: string
  /** 危险操作（不可逆）：确认钮用 danger 变体 + 标题带警示图标。 */
  danger?: boolean
  onConfirm: () => void
}

// 通用确认弹层：替代 window.confirm 的站内形态（原生 confirm 无法套 Token、
// 在部分嵌入环境行为不一致）。取摆在 Confirm 前——首个可聚焦元素落在取消侧，
// 危险操作的安全默认是「不动手」。Esc/遮罩点击由 Radix 统一映射为取消。
export function ConfirmDialog({ open, onOpenChange, title, message, confirmLabel, danger, onConfirm }: ConfirmDialogProps) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content
          role="alertdialog"
          aria-modal
          className="tac-modal fixed left-1/2 top-1/2 w-[360px] max-w-[calc(100vw-48px)] -translate-x-1/2 -translate-y-1/2 rounded-xl border border-border bg-surface p-5 shadow-lg outline-none"
        >
          <Dialog.Title className="flex items-center gap-2 text-sm font-semibold text-text">
            {danger && <AlertTriangle className="h-4 w-4 text-sev-critical" aria-hidden />}
            {title}
          </Dialog.Title>
          <Dialog.Description className="mt-2 text-[12.5px] leading-relaxed text-text-secondary">
            {message}
          </Dialog.Description>
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="secondary" size="sm" onClick={() => onOpenChange(false)}>
              取消
            </Button>
            <Button
              variant={danger ? 'danger' : 'primary'}
              size="sm"
              onClick={() => {
                onConfirm()
                onOpenChange(false)
              }}
            >
              {confirmLabel}
            </Button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
