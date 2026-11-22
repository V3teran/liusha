import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import * as Dialog from '@radix-ui/react-dialog'
import { useReturnFocus } from './useReturnFocus'

// 以 state 打开、没有 Radix Trigger 的弹层：关闭后焦点回到打开它的按钮。
function Harness({ withHook }: { withHook: boolean }) {
  const [open, setOpen] = useState(false)
  const returnFocus = useReturnFocus(open)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        打开
      </button>
      <Dialog.Root open={open} onOpenChange={setOpen}>
        <Dialog.Portal>
          <Dialog.Content aria-describedby={undefined} onCloseAutoFocus={withHook ? returnFocus : undefined}>
            <Dialog.Title>弹层</Dialog.Title>
            <button type="button">内部</button>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  )
}

describe('useReturnFocus', () => {
  it('Esc 关闭后焦点回到打开弹层的按钮', async () => {
    render(<Harness withHook />)
    const trigger = screen.getByRole('button', { name: '打开' })
    await userEvent.click(trigger)
    await screen.findByRole('dialog')
    await waitFor(() => expect(document.activeElement).not.toBe(trigger))
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(document.activeElement).toBe(trigger)
  })

  it('对照：不接钩子时焦点落到 body（Radix 只还给 Trigger）', async () => {
    render(<Harness withHook={false} />)
    await userEvent.click(screen.getByRole('button', { name: '打开' }))
    await screen.findByRole('dialog')
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(document.activeElement).toBe(document.body)
  })
})
