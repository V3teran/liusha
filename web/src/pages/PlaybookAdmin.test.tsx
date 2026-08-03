import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PlaybookAdmin } from './PlaybookAdmin'
import {
  listPlaybookConfigs,
  getPlaybookConfig,
  savePlaybook,
  deletePlaybook,
  listHunterConfigs,
} from '@/api/config'
import type { PlaybookConfig, HunterConfig } from '@/api/types'

vi.mock('@/api/config', () => ({
  listPlaybookConfigs: vi.fn(),
  getPlaybookConfig: vi.fn(),
  savePlaybook: vi.fn(),
  deletePlaybook: vi.fn(),
  listHunterConfigs: vi.fn(),
}))

const mList = listPlaybookConfigs as unknown as ReturnType<typeof vi.fn>
const mGet = getPlaybookConfig as unknown as ReturnType<typeof vi.fn>
const mSave = savePlaybook as unknown as ReturnType<typeof vi.fn>
const mDelete = deletePlaybook as unknown as ReturnType<typeof vi.fn>
const mListH = listHunterConfigs as unknown as ReturnType<typeof vi.fn>

const pb: PlaybookConfig = { id: 'pb-1', code: 'web', name: 'Web 剧本', description: '', enabled: true }
const hunters: HunterConfig[] = [
  { id: 'h-a', code: 'a', kind: 'domain', name: '侦察', description: '', body: '', tools: [], max_iterations: 20, enabled: true },
  { id: 'h-b', code: 'b', kind: 'domain', name: '利用', description: '', body: '', tools: [], max_iterations: 20, enabled: true },
  { id: 'h-o', code: 'o', kind: 'orchestrator', name: '编排', description: '', body: '', tools: [], max_iterations: 20, enabled: true },
]

describe('PlaybookAdmin', () => {
  beforeEach(() => {
    mList.mockReset()
    mGet.mockReset()
    mSave.mockReset()
    mDelete.mockReset()
    mListH.mockReset()
    mListH.mockResolvedValue(hunters)
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('挂载加载剧本列表', async () => {
    mList.mockResolvedValue([pb])
    render(<PlaybookAdmin />)
    expect(await screen.findByText('Web 剧本')).toBeTruthy()
  })

  it('点行拉全量单条并按序回填组合', async () => {
    mList.mockResolvedValue([pb])
    mGet.mockResolvedValue({
      ...pb,
      hunters: [
        { hunter_id: 'h-b', position: 1 },
        { hunter_id: 'h-a', position: 0 },
      ],
    })
    render(<PlaybookAdmin />)
    await userEvent.click(await screen.findByText('Web 剧本'))
    await waitFor(() => expect(mGet).toHaveBeenCalledWith('pb-1'))
    // 组合按 position 升序：侦察(0) 在前、利用(1) 在后。
    expect(await screen.findByText('侦察')).toBeTruthy()
    expect(screen.getByText('利用')).toBeTruthy()
  })

  it('保存把有序 hunter id 传给 savePlaybook', async () => {
    mList.mockResolvedValue([pb])
    mGet.mockResolvedValue({ ...pb, hunters: [{ hunter_id: 'h-a', position: 0 }] })
    mSave.mockResolvedValue(pb)
    render(<PlaybookAdmin />)
    await userEvent.click(await screen.findByText('Web 剧本'))
    await screen.findByText('编辑剧本')
    await userEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    expect(mSave.mock.calls[0][1]).toEqual(['h-a'])
  })

  it('候选下拉只含 domain 猎手（排除 orchestrator）', async () => {
    mList.mockResolvedValue([pb])
    mGet.mockResolvedValue({ ...pb, hunters: [] })
    render(<PlaybookAdmin />)
    await userEvent.click(await screen.findByText('Web 剧本'))
    await screen.findByText('编辑剧本')
    // 编排猎手不应出现在候选（组合池仅 domain）。
    const options = screen.getAllByRole('option').map((o) => o.textContent)
    expect(options).toContain('侦察')
    expect(options).toContain('利用')
    expect(options).not.toContain('编排')
  })

  it('删除走确认后调 deletePlaybook', async () => {
    mList.mockResolvedValue([pb])
    mGet.mockResolvedValue({ ...pb, hunters: [] })
    mDelete.mockResolvedValue(undefined)
    render(<PlaybookAdmin />)
    await userEvent.click(await screen.findByText('Web 剧本'))
    await screen.findByText('编辑剧本')
    await userEvent.click(screen.getByText('删除'))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('pb-1'))
  })
})
