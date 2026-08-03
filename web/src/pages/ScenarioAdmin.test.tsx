import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ScenarioAdmin } from './ScenarioAdmin'
import { listScenarioConfigs, saveScenario, deleteScenario, listPlaybookConfigs } from '@/api/config'
import type { ScenarioConfig, PlaybookConfig } from '@/api/types'

vi.mock('@/api/config', () => ({
  listScenarioConfigs: vi.fn(),
  saveScenario: vi.fn(),
  deleteScenario: vi.fn(),
  listPlaybookConfigs: vi.fn(),
}))

const mListSc = listScenarioConfigs as unknown as ReturnType<typeof vi.fn>
const mSave = saveScenario as unknown as ReturnType<typeof vi.fn>
const mDelete = deleteScenario as unknown as ReturnType<typeof vi.fn>
const mListPb = listPlaybookConfigs as unknown as ReturnType<typeof vi.fn>

function sc(o: Partial<ScenarioConfig> = {}): ScenarioConfig {
  return {
    id: 's1',
    code: 'web_app',
    name: 'Web 渗透',
    description: '',
    instruction: '',
    domain: 'web',
    engine: 'swarm',
    playbook_id: 'pb-1',
    enabled: true,
    ...o,
  }
}
const pb: PlaybookConfig = { id: 'pb-1', code: 'p', name: 'Web 剧本', description: '', enabled: true }

describe('ScenarioAdmin', () => {
  beforeEach(() => {
    mListSc.mockReset()
    mSave.mockReset()
    mDelete.mockReset()
    mListPb.mockReset()
    mListPb.mockResolvedValue([pb])
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
})
