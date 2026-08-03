import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RolePicker } from './RolePicker'
import { listRoles } from '@/api/client'
import type { Role } from '@/api/types'

vi.mock('@/api/client', () => ({
  listRoles: vi.fn(),
}))

const roles: Role[] = [
  { id: 'r-active-1', name: '主动角色1', description: '', mode: 'active' },
  { id: 'r-active-2', name: '主动角色2', description: '', mode: 'active' },
  { id: 'r-passive-1', name: '被动角色1', description: '', mode: 'passive' },
]

describe('RolePicker', () => {
  beforeEach(() => {
    vi.mocked(listRoles).mockReset()
  })

  it('挂载时拉取角色并渲染为 options', async () => {
    vi.mocked(listRoles).mockResolvedValue(roles)
    render(<RolePicker value="" onChange={vi.fn()} />)

    await waitFor(() => {
      expect(screen.getAllByRole('option').length).toBeGreaterThan(1)
    })
    expect(screen.getByText('主动角色1')).toBeTruthy()
    expect(screen.getByText('主动角色2')).toBeTruthy()
    expect(screen.getByText('被动角色1')).toBeTruthy()
  })

  it('按 mode 过滤：只列出匹配 mode 的角色', async () => {
    vi.mocked(listRoles).mockResolvedValue(roles)
    render(<RolePicker value="r-active-1" onChange={vi.fn()} mode="active" />)

    await waitFor(() => {
      expect(screen.getByText('主动角色1')).toBeTruthy()
    })
    expect(screen.getByText('主动角色2')).toBeTruthy()
    expect(screen.queryByText('被动角色1')).toBeFalsy()
  })

  it('value 为空时自动选中过滤后的第一个角色', async () => {
    vi.mocked(listRoles).mockResolvedValue(roles)
    const onChange = vi.fn()
    render(<RolePicker value="" onChange={onChange} mode="passive" />)

    await waitFor(() => {
      expect(onChange).toHaveBeenCalledWith('r-passive-1')
    })
  })

  it('value 非空时不自动选择', async () => {
    vi.mocked(listRoles).mockResolvedValue(roles)
    const onChange = vi.fn()
    render(<RolePicker value="r-active-2" onChange={onChange} mode="active" />)

    await waitFor(() => {
      expect(listRoles).toHaveBeenCalled()
    })
    expect(onChange).not.toHaveBeenCalled()
  })

  it('用户选择角色时触发 onChange', async () => {
    vi.mocked(listRoles).mockResolvedValue(roles)
    const onChange = vi.fn()
    render(<RolePicker value="r-active-1" onChange={onChange} mode="active" />)

    await waitFor(() => {
      expect(screen.getByText('主动角色2')).toBeTruthy()
    })
    const select = screen.getByRole('combobox') as HTMLSelectElement
    await userEvent.selectOptions(select, 'r-active-2')
    expect(onChange).toHaveBeenCalledWith('r-active-2')
  })
})
