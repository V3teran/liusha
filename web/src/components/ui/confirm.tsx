import type { ReactNode } from 'react'
import { create } from 'zustand'
import { ConfirmDialog } from './ConfirmDialog'

/**
 * 可等待的确认：`if (!(await confirmAction({...}))) return`。站内 ConfirmDialog 的命令式入口，
 * 用在事件处理函数里（删除、恢复出厂、丢弃未保存的修改）——不必在每个调用点各养一份开关状态。
 * 同一时刻至多一个确认；新的请求到来时，上一个按取消处理。
 */

export interface ConfirmOptions {
  title: string
  message: ReactNode
  /** 确认按钮文案：具体动词，与触发动作同名（「确认删除」「恢复出厂」「丢弃修改」）。 */
  confirmLabel: string
  /** 不可逆操作：确认钮用 danger 变体、标题带警示图标。 */
  danger?: boolean
}

interface ConfirmRequest extends ConfirmOptions {
  resolve: (ok: boolean) => void
  /** 发起确认时的焦点：确认框以代码打开、没有触发按钮，关闭后焦点还到这里（WCAG 焦点归还） */
  returnFocus: HTMLElement | null
}

interface ConfirmStore {
  request: ConfirmRequest | null
  /** 最近一次请求：关闭动画期间弹层仍显示它的文案（不闪成空白），关闭后据它归还焦点 */
  shown: ConfirmRequest | null
  open: (req: ConfirmRequest) => void
  settle: (ok: boolean) => void
}

export const useConfirmStore = create<ConfirmStore>((set, get) => ({
  request: null,
  shown: null,
  open: (req) => {
    get().request?.resolve(false)
    set({ request: req, shown: req })
  },
  settle: (ok) => {
    get().request?.resolve(ok)
    set({ request: null })
  },
}))

export function confirmAction(options: ConfirmOptions): Promise<boolean> {
  const active = document.activeElement
  const returnFocus = active instanceof HTMLElement && active !== document.body ? active : null
  return new Promise((resolve) => useConfirmStore.getState().open({ ...options, resolve, returnFocus }))
}

/** 确认弹层的宿主：挂在应用根部一次。 */
export function ConfirmHost() {
  const open = useConfirmStore((s) => s.request !== null)
  const shown = useConfirmStore((s) => s.shown)
  const settle = useConfirmStore((s) => s.settle)
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(next) => !next && settle(false)}
      title={shown?.title ?? ''}
      message={shown?.message ?? ''}
      confirmLabel={shown?.confirmLabel ?? ''}
      danger={shown?.danger}
      onConfirm={() => settle(true)}
      returnFocusTo={shown?.returnFocus}
    />
  )
}

/** 关闭有未保存修改的编辑器前的确认（各编辑抽屉共用同一文案）。 */
export function confirmDiscard(): Promise<boolean> {
  return confirmAction({
    title: '丢弃未保存的修改',
    message: '关闭后，这次的修改不会保留。',
    confirmLabel: '丢弃修改',
    danger: true,
  })
}
