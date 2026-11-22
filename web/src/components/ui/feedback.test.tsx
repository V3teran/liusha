import { describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConfirmHost, confirmAction } from './confirm'
import { Toaster, toast } from './toast'
import { ErrorState } from './ErrorState'
import { LoadingState } from './LoadingState'
import { InlineError } from './InlineError'

const OPTS = { title: '删除文件', message: '删除 a.txt？', confirmLabel: '确认删除', danger: true }

describe('confirmAction', () => {
  it('点确认按钮解析为 true，弹层关闭', async () => {
    render(<ConfirmHost />)
    let result: Promise<boolean>
    act(() => {
      result = confirmAction(OPTS)
    })
    expect(await screen.findByRole('alertdialog')).toHaveTextContent('删除 a.txt？')
    await userEvent.click(screen.getByRole('button', { name: '确认删除' }))
    await expect(result!).resolves.toBe(true)
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
  })

  it('取消、Esc 解析为 false', async () => {
    render(<ConfirmHost />)
    let first: Promise<boolean>
    act(() => {
      first = confirmAction(OPTS)
    })
    await userEvent.click(await screen.findByRole('button', { name: '取消' }))
    await expect(first!).resolves.toBe(false)

    let second: Promise<boolean>
    act(() => {
      second = confirmAction(OPTS)
    })
    await screen.findByRole('alertdialog')
    await userEvent.keyboard('{Escape}')
    await expect(second!).resolves.toBe(false)
  })

  it('关闭后焦点还给发起确认时的元素', async () => {
    render(
      <>
        <button type="button">删除</button>
        <ConfirmHost />
      </>,
    )
    const trigger = screen.getByRole('button', { name: '删除' })
    trigger.focus()
    let result: Promise<boolean>
    act(() => {
      result = confirmAction(OPTS)
    })
    await userEvent.click(await screen.findByRole('button', { name: '取消' }))
    await expect(result!).resolves.toBe(false)
    await waitFor(() => expect(document.activeElement).toBe(trigger))
  })

  it('新的确认请求到来时，上一个按取消处理', async () => {
    render(<ConfirmHost />)
    let first: Promise<boolean>
    let second: Promise<boolean>
    act(() => {
      first = confirmAction(OPTS)
    })
    act(() => {
      second = confirmAction({ ...OPTS, message: '删除 b.txt？' })
    })
    await expect(first!).resolves.toBe(false)
    expect(await screen.findByRole('alertdialog')).toHaveTextContent('删除 b.txt？')
    await userEvent.click(screen.getByRole('button', { name: '确认删除' }))
    await expect(second!).resolves.toBe(true)
  })
})

describe('toast', () => {
  it('失败通知显示原因，可手动关闭', async () => {
    render(<Toaster />)
    act(() => toast.error('删除对话失败，请重试'))
    expect(await screen.findByText('删除对话失败，请重试')).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: '关闭通知' }))
    await waitFor(() => expect(screen.queryByText('删除对话失败，请重试')).toBeNull())
  })
})

describe('ErrorState / LoadingState / InlineError', () => {
  it('错误态带原因与重试，重试回调被调用', async () => {
    const onRetry = vi.fn()
    render(<ErrorState message="网络断开" onRetry={onRetry} />)
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('加载失败')
    expect(alert).toHaveTextContent('网络断开')
    await userEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('没有重试回调时不放重试按钮', () => {
    render(<ErrorState title="对话不存在" />)
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('加载态以 status 播报，就地错误以 alert 播报', () => {
    render(
      <>
        <LoadingState label="编辑器加载中…" />
        <InlineError>保存失败</InlineError>
      </>,
    )
    expect(screen.getByRole('status')).toHaveTextContent('编辑器加载中…')
    expect(screen.getByRole('alert')).toHaveTextContent('保存失败')
  })
})
