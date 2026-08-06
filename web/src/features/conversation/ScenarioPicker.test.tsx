import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ScenarioPicker } from './ScenarioPicker'
import { listScenarios } from '@/api/client'
import type { ScenarioConfig } from '@/api/types'

vi.mock('@/api/client', () => ({
  listScenarios: vi.fn(),
}))

function sc(o: Partial<ScenarioConfig>): ScenarioConfig {
  return {
    id: '',
    code: '',
    name: '',
    description: '',
    instruction: '',
    engine: 'swarm',
    solo_hunter_id: '',
    enabled: true,
    ...o,
  }
}

const scenarios: ScenarioConfig[] = [
  sc({ id: 'u-1', code: 'web-scan', name: 'Web 漏洞扫描' }),
  sc({ id: 'u-2', code: 'ctf', name: 'CTF 夺旗' }),
  sc({ id: 'u-3', code: 'traffic-analysis', name: '流量分析' }),
]

describe('ScenarioPicker', () => {
  beforeEach(() => {
    vi.mocked(listScenarios).mockReset()
  })

  it('挂载时拉取场景并全部渲染为 options（含停用，不按 mode 过滤）', async () => {
    vi.mocked(listScenarios).mockResolvedValue(scenarios)
    render(<ScenarioPicker value="" onChange={vi.fn()} />)

    await waitFor(() => {
      expect(screen.getByText('Web 漏洞扫描')).toBeTruthy()
    })
    expect(screen.getByText('CTF 夺旗')).toBeTruthy()
    expect(screen.getByText('流量分析')).toBeTruthy()
  })

  it('停用场景照常渲染但 option 置灰不可选（可见 ≠ 可用）', async () => {
    vi.mocked(listScenarios).mockResolvedValue([
      sc({ id: 'u-1', code: 'web-scan', name: 'Web 漏洞扫描' }),
      sc({ id: 'u-2', code: 'ctf', name: 'CTF 夺旗', enabled: false }),
    ])
    render(<ScenarioPicker value="web-scan" onChange={vi.fn()} />)

    const off = (await screen.findByText(/CTF 夺旗/)) as HTMLOptionElement
    expect(off.disabled).toBe(true)
    expect(off.textContent).toContain('已停用')
    const on = screen.getByText('Web 漏洞扫描') as HTMLOptionElement
    expect(on.disabled).toBe(false)
  })

  it('value 为空时自动选中第一个「启用」场景的 code（跳过停用）', async () => {
    vi.mocked(listScenarios).mockResolvedValue([
      sc({ id: 'u-0', code: 'off-one', name: '停用场景', enabled: false }),
      sc({ id: 'u-1', code: 'web-scan', name: 'Web 漏洞扫描' }),
    ])
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
