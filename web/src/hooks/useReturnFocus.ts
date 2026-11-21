import { useCallback, useLayoutEffect, useRef } from 'react'

/**
 * 受控打开的弹层（没有 Radix Trigger）关闭后把焦点还给打开它时的焦点元素（WCAG 2.4.3）。
 * Radix 只会把焦点还给 Trigger；由按钮、表格行等以 state 打开的弹层没有 Trigger，
 * 关闭后焦点落到 body，键盘用户得从页首重新 Tab。
 *
 * 用法：`const returnFocus = useReturnFocus(open)`，传给 `Dialog.Content` 的 `onCloseAutoFocus`。
 * 记录发生在 layout effect：它先于 Radix 在 effect 里把焦点移进弹层，读到的还是打开前的焦点。
 */
export function useReturnFocus(open: boolean): (event: Event) => void {
  const returnTo = useRef<HTMLElement | null>(null)
  useLayoutEffect(() => {
    if (!open) return
    const active = document.activeElement
    returnTo.current = active instanceof HTMLElement && active !== document.body ? active : null
  }, [open])
  return useCallback((event: Event) => {
    const el = returnTo.current
    if (!el?.isConnected) return
    event.preventDefault()
    el.focus()
  }, [])
}
