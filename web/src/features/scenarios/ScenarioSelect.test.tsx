import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Composer } from '@/features/conversation/Composer'
import { followUp, startChat } from '@/api/conversations'
import { listScenarios } from '@/api/config'
import type { ScenarioListItem } from '@/api/types'
import { ScenarioSelect, selectableScenarios } from './ScenarioSelect'

vi.mock('@/api/conversations', () => ({ followUp: vi.fn(), startChat: vi.fn() }))
vi.mock('@/api/config', () => ({ listScenarios: vi.fn() }))

const sc = (o: Partial<ScenarioListItem>): ScenarioListItem => ({
  code: 'x', name: 'x', description: '', body: 'b', enabled: true, is_builtin: true, is_default: false, customized: [],
  shadows_builtin: false, ...o,
})

const SCENARIOS = [
  sc({ code: 'ctf', name: 'CTF' }),
  sc({ code: 'pentest', name: '渗透测试', is_default: true }),
  sc({ code: 'off', name: '停用的', enabled: false, is_builtin: false }),
]

function renderComposer(props: Partial<Parameters<typeof Composer>[0]> = {}) {
  vi.mocked(listScenarios).mockResolvedValue(SCENARIOS)
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <Composer scenarioControl={{ mode: 'pick' }} onStarted={vi.fn()} onAppended={vi.fn()} {...props} />
    </QueryClientProvider>,
  )
}

describe('ScenarioSelect', () => {
  it('只列启用的场景，缺省场景排第一', () => {
    expect(selectableScenarios(SCENARIOS).map((s) => s.code)).toEqual(['pentest', 'ctf'])
  })

  it('未选时不带场景（服务端落定缺省）', async () => {
    vi.mocked(startChat).mockResolvedValue({ conversation_id: 'c', task_id: 't' })
    renderComposer()
    await waitFor(() => expect(screen.getByRole('option', { name: 'CTF' })).toBeTruthy())
    await userEvent.type(screen.getByRole('textbox', { name: '消息输入' }), '扫一下')
    await userEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(startChat).toHaveBeenCalledWith('扫一下', [], undefined))
  })

  it('选中的场景已不可用时如实标注，不换成别的场景', async () => {
    vi.mocked(listScenarios).mockResolvedValue(SCENARIOS)
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <ScenarioSelect value="off" onChange={vi.fn()} aria-label="场景" />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(screen.getByRole('option', { name: 'off（已不可用）' })).toBeTruthy())
    expect((screen.getByRole('combobox', { name: '场景' }) as HTMLSelectElement).value).toBe('off')
  })

  it('切到别的对话后场景回到未选', async () => {
    vi.mocked(followUp).mockResolvedValue({ intent: 'accepted' })
    vi.mocked(listScenarios).mockResolvedValue(SCENARIOS)
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const ui = (convId: string) => (
      <QueryClientProvider client={qc}>
        <Composer convId={convId} scenarioControl={{ mode: 'pick' }} onStarted={vi.fn()} onAppended={vi.fn()} />
      </QueryClientProvider>
    )
    const { rerender } = render(ui('a'))
    await waitFor(() => expect(screen.getByRole('option', { name: 'CTF' })).toBeTruthy())
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '任务场景' }), 'ctf')
    rerender(ui('b'))
    expect((screen.getByRole('combobox', { name: '任务场景' }) as HTMLSelectElement).value).toBe('pentest')
    await userEvent.type(screen.getByRole('textbox', { name: '消息输入' }), '看看')
    await userEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(followUp).toHaveBeenCalledWith('b', '看看', [], undefined))
  })

  it('新对话发起后保留选择一次；之后来回切换对话都回到未选（新 → A → B → A）', async () => {
    vi.mocked(startChat).mockResolvedValue({ conversation_id: 'A', task_id: '' })
    vi.mocked(listScenarios).mockResolvedValue(SCENARIOS)
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const ui = (convId?: string) => (
      <QueryClientProvider client={qc}>
        <Composer convId={convId} scenarioControl={{ mode: 'pick' }} onStarted={vi.fn()} onAppended={vi.fn()} />
      </QueryClientProvider>
    )
    const { rerender } = render(ui())
    const pick = () => screen.getByRole('combobox', { name: '任务场景' }) as HTMLSelectElement
    await waitFor(() => expect(screen.getByRole('option', { name: 'CTF' })).toBeTruthy())
    await userEvent.selectOptions(pick(), 'ctf')
    await userEvent.type(screen.getByRole('textbox', { name: '消息输入' }), '聊聊')
    await userEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(startChat).toHaveBeenCalledWith('聊聊', [], 'ctf'))
    rerender(ui('A'))
    expect(pick().value).toBe('ctf') // 刚发起的对话：同一份草稿的延续
    rerender(ui('B'))
    expect(pick().value).toBe('pentest')
    await userEvent.selectOptions(pick(), '')
    rerender(ui('A'))
    expect(pick().value).toBe('pentest') // 豁免只用一次，不把 B 的选择带回 A
  })

  it('对话发起时缺省显示渗透测试，末位可选无场景，所选场景随消息提交', async () => {
    vi.mocked(startChat).mockResolvedValue({ conversation_id: 'c', task_id: 't' })
    renderComposer()
    const select = screen.getByRole('combobox', { name: '任务场景' })
    await waitFor(() => expect(screen.getByRole('option', { name: 'CTF' })).toBeTruthy())
    expect((select as HTMLSelectElement).value).toBe('pentest')
    expect(screen.queryByRole('option', { name: '停用的' })).toBeNull()
    const options = screen.getAllByRole('option').map((o) => o.textContent)
    expect(options[options.length - 1]).toBe('无场景')

    await userEvent.selectOptions(select, 'ctf')
    await userEvent.type(screen.getByRole('textbox', { name: '消息输入' }), '解题')
    await userEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(startChat).toHaveBeenCalledWith('解题', [], 'ctf'))
  })

  it('尚未绑定任务的会话追加消息时带上场景；选「无场景」提交空串', async () => {
    vi.mocked(followUp).mockResolvedValue({ intent: 'accepted' })
    renderComposer({ convId: 'c1' })
    await waitFor(() => expect(screen.getByRole('option', { name: 'CTF' })).toBeTruthy())
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '任务场景' }), '')
    await userEvent.type(screen.getByRole('textbox', { name: '消息输入' }), '看看')
    await userEvent.click(screen.getByRole('button', { name: '发送' }))
    await waitFor(() => expect(followUp).toHaveBeenCalledWith('c1', '看看', [], ''))
  })

  it('未要求场景控件时不显示', () => {
    renderComposer({ convId: 'c1', scenarioControl: undefined })
    expect(screen.queryByRole('combobox', { name: '任务场景' })).toBeNull()
  })

  it('已绑定任务的会话显示只读场景（快照名，不可改）', () => {
    renderComposer({ convId: 'c1', scenarioControl: { mode: 'locked', label: 'CTF' } })
    const sel = screen.getByRole('combobox', { name: '任务场景（已随任务固定）' }) as HTMLSelectElement
    expect(sel.disabled).toBe(true)
    expect(sel.selectedOptions[0].textContent).toBe('CTF')
  })
})

describe('ScenarioSelect 缺省场景不可用', () => {
  it('缺省场景被停用或删除时，未选即显示无场景', async () => {
    vi.mocked(listScenarios).mockResolvedValue([sc({ code: 'ctf', name: 'CTF' })])
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <ScenarioSelect value={null} onChange={vi.fn()} aria-label="场景" />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(screen.getByRole('option', { name: 'CTF' })).toBeTruthy())
    expect((screen.getByRole('combobox', { name: '场景' }) as HTMLSelectElement).value).toBe('')
  })
})
