import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'

// 弹层右上角的关闭按钮（对话框、抽屉共用）：32px 命中区（WCAG 2.5.8 要求 ≥24px）、
// 16px 图标、三级灰静息 / 悬停加底提亮，焦点环走全站 :focus-visible。
export function DialogClose({ disabled, className }: { disabled?: boolean; className?: string }) {
  return (
    <Dialog.Close asChild>
      <button
        type="button"
        aria-label="关闭"
        disabled={disabled}
        className={cn(
          'flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-md text-text-tertiary transition-colors',
          'hover:bg-surface-2 hover:text-text disabled:opacity-50',
          className,
        )}
      >
        <X aria-hidden className="h-4 w-4" />
      </button>
    </Dialog.Close>
  )
}
