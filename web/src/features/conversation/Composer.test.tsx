import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Composer } from './Composer'
import { followUp, startChat, listScenarios } from '@/api/client'
import { useConversationStore } from '@/stores/conversation'

vi.mock('@/api/client', () => ({
  followUp: vi.fn(),
  startChat: vi.fn(),
  listScenarios: vi.fn(),
}))

describe('Composer', () => {
  beforeEach(() => {
    useConversationStore.getState().reset()
    vi.mocked(followUp).mockReset()
    vi.mocked(startChat).mockReset()
    vi.mocked(listScenarios).mockReset()
    vi.mocked(listScenarios).mockResolvedValue([])
  })

  it('渲染 ScenarioPicker（新会话与追加都常驻，供纯聊天升级为 action 用）', () => {
    render(<Composer onStarted={vi.fn()} onAppended={vi.fn()} onStop={vi.fn()} />)
    expect(screen.getByRole('combobox')).toBeTruthy()
  })

  it('有 convId 时仍渲染 ScenarioPicker（升级路径需要当前场景）', () => {
    render(<Composer convId="c1" onStarted={vi.fn()} onAppended={vi.fn()} onStop={vi.fn()} />)
    expect(screen.getByRole('combobox')).toBeTruthy()
  })

  it('无 convId 发送时调用 startChat 并触发 onStarted', async () => {
    vi.mocked(startChat).mockResolvedValue({ conversation_id: 'new-conv', scan_id: 's1' })
    const onStarted = vi.fn()
    render(<Composer onStarted={onStarted} onAppended={vi.fn()} onStop={vi.fn()} />)

    const textarea = screen.getByPlaceholderText(/描述要扫的目标/)
    await userEvent.type(textarea, '扫一下 example.com')
    const sendBtn = screen.getByRole('button', { name: '发起扫描' })
    await userEvent.click(sendBtn)

    await waitFor(() => {
      expect(startChat).toHaveBeenCalledWith('扫一下 example.com', '')
      expect(onStarted).toHaveBeenCalledWith('new-conv')
    })
  })

  it('有 convId 发送时调用 followUp 并触发 onAppended（携带发送前的 seq 快照）', async () => {
    useConversationStore.getState().ingest({
      Seq: 7,
      ID: 'm1',
      ConversationID: 'c1',
      Role: 'assistant',
      Kind: 'message',
      Content: 'x',
      Metadata: null,
      CreatedAt: '',
    })
    vi.mocked(followUp).mockResolvedValue({ intent: 'scan', scan_id: 's1' })
    const onAppended = vi.fn()
    render(<Composer convId="c1" onStarted={vi.fn()} onAppended={onAppended} onStop={vi.fn()} />)

    const textarea = screen.getByPlaceholderText(/继续提问/)
    await userEvent.type(textarea, '继续扫')
    const sendBtn = screen.getByRole('button', { name: '追加' })
    await userEvent.click(sendBtn)

    await waitFor(() => {
      expect(followUp).toHaveBeenCalledWith('c1', '继续扫', '')
      expect(onAppended).toHaveBeenCalledWith(7)
    })
  })

  it('followUp 前快照的 seq 不受发送过程中 store lastSeq 增长影响（竞态修复）', async () => {
    useConversationStore.getState().ingest({
      Seq: 3,
      ID: 'm1',
      ConversationID: 'c1',
      Role: 'assistant',
      Kind: 'message',
      Content: 'x',
      Metadata: null,
      CreatedAt: '',
    })
    let resolveFollowUp!: (v: { intent: string }) => void
    vi.mocked(followUp).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveFollowUp = resolve
        }),
    )
    const onAppended = vi.fn()
    render(<Composer convId="c1" onStarted={vi.fn()} onAppended={onAppended} onStop={vi.fn()} />)

    const textarea = screen.getByPlaceholderText(/继续提问/)
    await userEvent.type(textarea, '继续扫')
    await userEvent.click(screen.getByRole('button', { name: '追加' }))

    // 发送过程中，SSE 把 lastSeq 推高（模拟并发到达的新事件）。
    useConversationStore.getState().ingest({
      Seq: 9,
      ID: 'm2',
      ConversationID: 'c1',
      Role: 'tool',
      Kind: 'event',
      Content: '',
      Metadata: null,
      CreatedAt: '',
    })

    resolveFollowUp({ intent: 'scan' })
    await waitFor(() => {
      expect(onAppended).toHaveBeenCalledWith(3) // 快照值，不是 9
    })
  })

  it('followUp 返回 409 busy 时显示提示', async () => {
    const err = new Error('扫描进行中') as Error & { busy?: boolean }
    err.busy = true
    vi.mocked(followUp).mockRejectedValue(err)
    render(<Composer convId="c1" onStarted={vi.fn()} onAppended={vi.fn()} onStop={vi.fn()} />)

    const textarea = screen.getByPlaceholderText(/继续提问/)
    await userEvent.type(textarea, '继续扫')
    await userEvent.click(screen.getByRole('button', { name: '追加' }))

    await waitFor(() => {
      expect(screen.getByText('扫描进行中，先点停止再发')).toBeTruthy()
    })
  })

  it('scanning=true 且有 convId 时仍可发送（不再前端一刀切拦截，交给后端按意图判定）', async () => {
    // passive 会话的 task 常年 active，若前端按 scanning 拦，QA 类追问会被永久锁死——
    // 这里验证 scanning=true 时输入框/发送按钮仍可用，且真的调用了 followUp。
    vi.mocked(followUp).mockResolvedValue({ intent: 'qa' })
    render(<Composer convId="c1" scanning onStarted={vi.fn()} onAppended={vi.fn()} onStop={vi.fn()} />)

    const textarea = screen.getByPlaceholderText(/继续提问/)
    await userEvent.type(textarea, '这批流量里有没有可疑的')
    await userEvent.click(screen.getByRole('button', { name: '追加' }))

    await waitFor(() => {
      expect(followUp).toHaveBeenCalledWith('c1', '这批流量里有没有可疑的', '')
    })
  })

  it('scanning=true 时后端仍以 409 busy 拒绝 action 类指令，前端展示提示', async () => {
    const err = new Error('扫描进行中') as Error & { busy?: boolean }
    err.busy = true
    vi.mocked(followUp).mockRejectedValue(err)
    render(<Composer convId="c1" scanning onStarted={vi.fn()} onAppended={vi.fn()} onStop={vi.fn()} />)

    const textarea = screen.getByPlaceholderText(/继续提问/)
    await userEvent.type(textarea, '继续扫')
    await userEvent.click(screen.getByRole('button', { name: '追加' }))

    await waitFor(() => {
      expect(screen.getByText('扫描进行中，先点停止再发')).toBeTruthy()
    })
  })

  it('scanning=true 时显示停止按钮，点击触发 onStop', async () => {
    const onStop = vi.fn()
    render(<Composer convId="c1" scanning onStarted={vi.fn()} onAppended={vi.fn()} onStop={onStop} />)
    await userEvent.click(screen.getByRole('button', { name: '停止扫描' }))
    expect(onStop).toHaveBeenCalled()
  })

  it('Enter 触发发送', async () => {
    vi.mocked(startChat).mockResolvedValue({ conversation_id: 'new-conv', scan_id: 's1' })
    const onStarted = vi.fn()
    render(<Composer onStarted={onStarted} onAppended={vi.fn()} onStop={vi.fn()} />)

    const textarea = screen.getByPlaceholderText(/描述要扫的目标/)
    await userEvent.type(textarea, '扫一下 example.com')
    await userEvent.keyboard('{Enter}')

    await waitFor(() => {
      expect(startChat).toHaveBeenCalled()
      expect(onStarted).toHaveBeenCalledWith('new-conv')
    })
  })

  it('Shift+Enter 换行，不触发发送', async () => {
    const onStarted = vi.fn()
    render(<Composer onStarted={onStarted} onAppended={vi.fn()} onStop={vi.fn()} />)

    const textarea = screen.getByPlaceholderText(/描述要扫的目标/) as HTMLTextAreaElement
    await userEvent.type(textarea, '第一行')
    await userEvent.keyboard('{Shift>}{Enter}{/Shift}')
    await userEvent.type(textarea, '第二行')

    expect(startChat).not.toHaveBeenCalled()
    expect(textarea.value).toBe('第一行\n第二行')
  })

  it('空 brief 不发送', async () => {
    render(<Composer onStarted={vi.fn()} onAppended={vi.fn()} onStop={vi.fn()} />)
    const sendBtn = screen.getByRole('button', { name: '发起扫描' })
    expect(sendBtn).toBeDisabled()
    await userEvent.click(sendBtn)
    expect(startChat).not.toHaveBeenCalled()
  })
})
