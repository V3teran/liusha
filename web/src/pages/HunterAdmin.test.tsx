import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HunterAdmin } from './HunterAdmin'
import { listHunterConfigs, saveHunter, deleteHunter } from '@/api/config'
import type { HunterConfig } from '@/api/types'

vi.mock('@/api/config', () => ({
  listHunterConfigs: vi.fn(),
  saveHunter: vi.fn(),
  deleteHunter: vi.fn(),
}))

const mList = listHunterConfigs as unknown as ReturnType<typeof vi.fn>
const mSave = saveHunter as unknown as ReturnType<typeof vi.fn>
const mDelete = deleteHunter as unknown as ReturnType<typeof vi.fn>

function h(o: Partial<HunterConfig> = {}): HunterConfig {
  return {
    id: 'h1',
    code: 'recon',
    kind: 'domain',
    name: '侦察猎手',
    description: '',
    body: 'charter',
    tools: ['http_get', 'dns_lookup'],
    max_iterations: 20,
    enabled: true,
    ...o,
  }
}

describe('HunterAdmin', () => {
  beforeEach(() => {
    mList.mockReset()
    mSave.mockReset()
    mDelete.mockReset()
    vi.spyOn(window, 'alert').mockImplementation(() => {})
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('挂载加载猎手列表', async () => {
    mList.mockResolvedValue([h()])
    render(<HunterAdmin />)
    expect(await screen.findByText('侦察猎手')).toBeTruthy()
  })

  it('tools 数组回填为每行一个', async () => {
    mList.mockResolvedValue([h()])
    render(<HunterAdmin />)
    await userEvent.click(await screen.findByText('侦察猎手'))
    await screen.findByText('编辑智能体')
    const ta = document.querySelectorAll('textarea')
    const values = Array.from(ta).map((t) => (t as HTMLTextAreaElement).value)
    expect(values).toContain('http_get\ndns_lookup')
  })

  it('保存时把每行文本的 tools 拆回数组', async () => {
    mList.mockResolvedValue([h()]) // 初值 tools=['http_get','dns_lookup']
    mSave.mockResolvedValue(h())
    render(<HunterAdmin />)
    await userEvent.click(await screen.findByText('侦察猎手'))
    await screen.findByText('编辑智能体')
    await userEvent.click(screen.getByText('保存'))
    await waitFor(() => expect(mSave).toHaveBeenCalled())
    expect(mSave.mock.calls[0][0].tools).toEqual(['http_get', 'dns_lookup'])
  })

  it('编排猎手显示编排徽章', async () => {
    mList.mockResolvedValue([h({ kind: 'orchestrator', name: '编排猎手' })])
    render(<HunterAdmin />)
    expect(await screen.findByText('编排')).toBeTruthy()
  })

  it('删除走确认后调 deleteHunter', async () => {
    mList.mockResolvedValue([h()])
    mDelete.mockResolvedValue(undefined)
    render(<HunterAdmin />)
    await userEvent.click(await screen.findByText('侦察猎手'))
    await userEvent.click(await screen.findByText('删除'))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('h1'))
  })
})
