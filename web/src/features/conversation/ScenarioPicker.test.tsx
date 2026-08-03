import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ScenarioPicker } from './ScenarioPicker'
import { listScenarios } from '@/api/client'
import type { Scenario } from '@/api/types'

vi.mock('@/api/client', () => ({
  listScenarios: vi.fn(),
}))

const scenarios: Scenario[] = [
  { id: 'u-1', code: 'web-scan', name: 'Web 漏洞扫描', description: '' },
  { id: 'u-2', code: 'ctf', name: 'CTF 夺旗', description: '' },
  { id: 'u-3', code: 'traffic-analysis', name: '流量分析', description: '' },
]

describe('ScenarioPicker', () => {
  beforeEach(() => {
    vi.mocked(listScenarios).mockReset()
  })

  it('挂载时拉取场景并全部渲染为 options（不按 mode 过滤）', async () => {
    vi.mocked(listScenarios).mockResolvedValue(scenarios)
    render(<ScenarioPicker value="" onChange={vi.fn()} />)

    await waitFor(() => {
      expect(screen.getByText('Web 漏洞扫描')).toBeTruthy()
    })
    expect(screen.getByText('CTF 夺旗')).toBeTruthy()
    expect(screen.getByText('流量分析')).toBeTruthy()
  })

  it('value 为空时自动选中第一个场景的 code', async () => {
    vi.mocked(listScenarios).mockResolvedValue(scenarios)
    const onChange = vi.fn()
    render(<ScenarioPicker value="" onChange={onChange} />)

    await waitFor(() => {
      expect(onChange).toHaveBeenCalledWith('web-scan')
    })
  })

  it('value 非空时不自动选择', async () => {
    vi.mocked(listScenarios).mockResolvedValue(scenarios)
    const onChange = vi.fn()
    render(<ScenarioPicker value="ctf" onChange={onChange} />)

    await waitFor(() => {
      expect(listScenarios).toHaveBeenCalled()
    })
    expect(onChange).not.toHaveBeenCalled()
  })

  it('用户选择场景时以 code 触发 onChange', async () => {
    vi.mocked(listScenarios).mockResolvedValue(scenarios)
    const onChange = vi.fn()
    render(<ScenarioPicker value="web-scan" onChange={onChange} />)

    await waitFor(() => {
      expect(screen.getByText('CTF 夺旗')).toBeTruthy()
    })
    const select = screen.getByRole('combobox') as HTMLSelectElement
    await userEvent.selectOptions(select, 'ctf')
    expect(onChange).toHaveBeenCalledWith('ctf')
  })
})
