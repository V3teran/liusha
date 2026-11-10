import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ConfirmDialog } from './ConfirmDialog'

// 确认弹层：受控开关 + 确认/取消回调的契约。Radix Portal 渲染在挂载节点之外，
// 断言统一走 role 查询。

describe('ConfirmDialog', () => {
  const mount = (onConfirm = vi.fn(), onOpenChange = vi.fn()) => {
    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="停止任务"
        message="任务将落终态，进行中的 agent 立即退出（可重新下发）。"
        confirmLabel="确认停止"
        danger
        onConfirm={onConfirm}
      />,
    )
    return { onConfirm, onOpenChange }
  }

  it('打开时呈现标题、正文与确认按钮', () => {
    mount()
    expect(screen.getByText('停止任务')).toBeTruthy()
    expect(screen.getByText('任务将落终态，进行中的 agent 立即退出（可重新下发）。')).toBeTruthy()
    expect(screen.getByRole('button', { name: '确认停止' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '取消' })).toBeTruthy()
  })

  it('点确认触发 onConfirm 并关闭', async () => {
    const { onConfirm, onOpenChange } = mount()
    screen.getByRole('button', { name: '确认停止' }).click()
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('点取消不触发 onConfirm，仅关闭', async () => {
    const { onConfirm, onOpenChange } = mount()
    screen.getByRole('button', { name: '取消' }).click()
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('非危险操作不渲染警示图标，确认钮非 danger 描边', () => {
    render(
      <ConfirmDialog
        open
        onOpenChange={vi.fn()}
        title="暂停任务"
        message="agent 将停止执行，进度与探索图保留，可随时继续。"
        confirmLabel="确认暂停"
        onConfirm={vi.fn()}
      />,
    )
    expect(document.querySelector('.lucide-alert-triangle')).toBeNull()
    expect(screen.getByRole('button', { name: '确认暂停' }).className).not.toContain('sev-critical')
  })
})
