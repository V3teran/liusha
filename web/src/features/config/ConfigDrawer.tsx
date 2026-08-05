import type { ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'

interface ConfigDrawerProps {
  open: boolean
  title: string // 抽屉标题（如「编辑场景」「新建猎手」）
  onOpenChange: (open: boolean) => void
  onSave: () => void
  onDelete?: () => void // 仅编辑态传（新建无可删）
  saving?: boolean
  saveDisabled?: boolean // 表单未过校验时禁用保存
  children: ReactNode // 表单主体
}

// 配置管理三资源共用的右侧编辑抽屉外壳（复用漏洞页 Radix Dialog 右滑模式）。
// 只管外壳：遮罩 + 右滑面板 + 头部标题/关闭 + 底部保存/删除；表单主体由各页 children 注入。
export function ConfigDrawer({
  open,
  title,
  onOpenChange,
  onSave,
  onDelete,
  saving = false,
  saveDisabled = false,
  children,
}: ConfigDrawerProps) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="tac-modal-overlay fixed inset-0 z-40 bg-black/40 backdrop-blur-[2px]" />
        <Dialog.Content className="tac-modal fixed left-1/2 top-1/2 z-50 flex max-h-[88vh] w-[820px] max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-xl border border-accent/30 bg-surface shadow-[var(--shadow-lg),var(--glow-accent)] focus:outline-none">
          <div className="tac-dots relative flex items-center justify-between border-b border-border px-5 py-4">
            <Dialog.Title className="font-mono text-base font-semibold text-text">{title}</Dialog.Title>
            <Dialog.Close className="mr-11 text-muted hover:text-text" aria-label="关闭">
              <X className="h-4 w-4" />
            </Dialog.Close>
          </div>

          <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-5 py-4.5">{children}</div>

          <div className="flex items-center gap-2.5 border-t border-border px-5 py-4">
            {onDelete && (
              <button
                type="button"
                onClick={onDelete}
                disabled={saving}
                className="rounded-lg border border-sev-critical/40 px-4 py-1.5 text-[13px] text-sev-critical transition-colors hover:bg-sev-critical/10 disabled:cursor-default disabled:opacity-40"
              >
                删除
              </button>
            )}
            <button
              type="button"
              onClick={onSave}
              disabled={saving || saveDisabled}
              className="ml-auto rounded-lg bg-accent px-5 py-1.5 text-[13px] text-white transition-all hover:bg-accent-hover hover:shadow-[var(--glow-accent-strong)] disabled:cursor-default disabled:opacity-40 disabled:hover:shadow-none"
            >
              {saving ? '保存中…' : '保存'}
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

// 表单字段行：label + 控件竖排，配置三页共用。
export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-xs font-medium text-muted">
        {label}
        {hint && <span className="ml-2 font-normal text-faint">{hint}</span>}
      </span>
      {children}
    </label>
  )
}

// 统一输入框样式（与漏洞抽屉一致）。聚焦态：emerald 描边 + 深色发散荧光晕（战术终端）。
export const INPUT_CLASS =
  'w-full rounded-md border border-border bg-surface px-2.5 py-1.5 text-[13px] text-text outline-none transition-shadow focus:border-accent focus:shadow-[var(--glow-accent)] disabled:cursor-not-allowed disabled:opacity-60'
