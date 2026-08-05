import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ScenarioAdmin } from './ScenarioAdmin'
import { listScenarios } from '@/api/client'
import { saveScenario, deleteScenario, listHunterConfigs } from '@/api/config'
import type { ScenarioConfig, HunterConfig } from '@/api/types'

vi.mock('@/api/client', () => ({
  listScenarios: vi.fn(),
}))

vi.mock('@/api/config', () => ({
  saveScenario: vi.fn(),
  deleteScenario: vi.fn(),
  listHunterConfigs: vi.fn(),
}))

const mListSc = listScenarios as unknown as ReturnType<typeof vi.fn>
const mSave = saveScenario as unknown as ReturnType<typeof vi.fn>
const mDelete = deleteScenario as unknown as ReturnType<typeof vi.fn>
const mListHunters = listHunterConfigs as unknown as ReturnType<typeof vi.fn>

function sc(o: Partial<ScenarioConfig> = {}): ScenarioConfig {
  return {
    id: 's1',
    code: 'web_app',
    name: 'Web 渗透',
    description: '',
    instruction: '',
    domain: 'web',
    engine: 'swarm',
    solo_hunter_id: '',
    enabled: true,
    ...o,
  }
}
const hunter: HunterConfig = {
  id: 'h-recon',
  code: 'recon',
  kind: 'domain',
  name: '侦察智能体',
  description: '',
  body: '',
  tools: [],
  cli_tools: [],
  max_iterations: 20,
  enabled: true,
}

describe('ScenarioAdmin', () => {
  beforeEach(() => {
    mListSc.mockReset()
    mSave.mockReset()
    mDelete.mockReset()
    mListHunters.mockReset()
    mListHunters.mockResolvedValue([hunter])
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('挂载加载场景列表', async () => {
    mListSc.mockResolvedValue([sc()])
    render(<ScenarioAdmin />)
    expect(await screen.findByText('Web 渗透')).toBeTruthy()
  })

  it('disabled 场景显示已停用徽章', async () => {
    mListSc.mockResolvedValue([sc({ enabled: false })])
    render(<ScenarioAdmin />)
    expect(await screen.findByText('已停用')).toBeTruthy()
  })

  it('空态提示', async () => {
    mListSc.mockResolvedValue([])
    render(<ScenarioAdmin />)
    expect(await screen.findByText(/暂无场景/)).toBeTruthy()
  })

  it('点行打开编辑抽屉并回填字段', async () => {
    mListSc.mockResolvedValue([sc()])
    render(<ScenarioAdmin />)
    await userEvent.click(await screen.findByText('Web 渗透'))
    expect(await screen.findByText('编辑场景')).toBeTruthy()
    expect((screen.getByDisplayValue('web_app') as HTMLInputElement).value).toBe('web_app')
  })

  it('编辑后保存调 saveScenario 并重载', async () => {
    mListSc.mockResolvedValue([sc()])
    mSave.mockResolvedValue(sc())
    render(<ScenarioAdmin />)
    await userEvent.click(await screen.findByText('Web 渗透'))
    await userEvent.click(await screen.findByText('保存'))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    expect(mListSc).toHaveBeenCalledTimes(2)
  })

  it('删除走确认后调 deleteScenario', async () => {
    mListSc.mockResolvedValue([sc()])
    mDelete.mockResolvedValue(undefined)
    render(<ScenarioAdmin />)
    await userEvent.click(await screen.findByText('Web 渗透'))
    await userEvent.click(await screen.findByText('删除'))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('s1'))
  })

  it('新建时 code/name 空则保存禁用', async () => {
    mListSc.mockResolvedValue([])
    render(<ScenarioAdmin />)
    await userEvent.click(await screen.findByText('新建'))
    expect((await screen.findByText('保存')).closest('button')).toHaveProperty('disabled', true)
  })

  it('solo 场景回填执行智能体选择器', async () => {
    mListSc.mockResolvedValue([sc({ engine: 'solo', solo_hunter_id: 'h-recon' })])
    render(<ScenarioAdmin />)
    await userEvent.click(await screen.findByText('Web 渗透'))
    expect(await screen.findByText('执行智能体')).toBeTruthy()
    // 选中项应为回填的领域智能体。
    const select = (await screen.findByText('侦察智能体')).closest('select') as HTMLSelectElement
    expect(select.value).toBe('h-recon')
  })

  it('swarm 场景显示无需指定提示', async () => {
    mListSc.mockResolvedValue([sc({ engine: 'swarm' })])
    render(<ScenarioAdmin />)
    await userEvent.click(await screen.findByText('Web 渗透'))
    expect(await screen.findByText(/运行期自动纳入全部启用的领域智能体/)).toBeTruthy()
  })
})
