import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Composer } from './Composer'
import { followUp, startChat } from '@/api/conversations'
import { uploadAttachment } from '@/api/attachments'
import { useConversationStore } from '@/stores/conversation'

vi.mock('@/api/conversations', () => ({
  followUp: vi.fn(),
  startChat: vi.fn(),
}))

vi.mock('@/api/attachments', () => ({
  uploadAttachment: vi.fn(),
}))

describe('Composer', () => {
  beforeEach(() => {
    useConversationStore.getState().reset()
    vi.mocked(followUp).mockReset()
    vi.mocked(startChat).mockReset()
  })

  it('无 convId 发送时调用 startChat 并触发 onStarted', async () => {
    vi.mocked(startChat).mockResolvedValue({ conversation_id: 'new-conv', task_id: 't-new' })
    const onStarted = vi.fn()
    render(<Composer onStarted={onStarted} onAppended={vi.fn()}  />)

    const textarea = screen.getByPlaceholderText(/描述要扫的目标/)
    await userEvent.type(textarea, '扫一下 example.com')
    const sendBtn = screen.getByRole('button', { name: '发送' })
    await userEvent.click(sendBtn)

    await waitFor(() => {
      expect(startChat).toHaveBeenCalledWith('扫一下 example.com', [])
      expect(onStarted).toHaveBeenCalledWith('new-conv')
    })
  })

  it('有 convId 发送时调用 followUp 并触发 onAppended（携带发送前的 seq 快照）', async () => {
    useConversationStore.getState().ingest({
      seq: 7,
      id: 'm1',
      conversation_id: 'c1',
      role: 'assistant',
      kind: 'message',
      content: 'x',
      metadata: null,
      created_at: '',
    })
    vi.mocked(followUp).mockResolvedValue({ intent: 'action' })
    const onAppended = vi.fn()
    render(<Composer convId="c1" onStarted={vi.fn()} onAppended={onAppended}  />)

    const textarea = screen.getByPlaceholderText(/继续提问/)
    await userEvent.type(textarea, '继续扫')
    const sendBtn = screen.getByRole('button', { name: '发送' })
    await userEvent.click(sendBtn)

    await waitFor(() => {
      expect(followUp).toHaveBeenCalledWith('c1', '继续扫', [])
      expect(onAppended).toHaveBeenCalledWith(7)
    })
  })

  it('followUp 前快照的 seq 不受发送过程中 store lastSeq 增长影响（竞态修复）', async () => {
    useConversationStore.getState().ingest({
      seq: 3,
      id: 'm1',
      conversation_id: 'c1',
      role: 'assistant',
      kind: 'message',
      content: 'x',
      metadata: null,
      created_at: '',
    })
    let resolveFollowUp!: (v: { intent: string }) => void
    vi.mocked(followUp).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveFollowUp = resolve
        }),
    )
    const onAppended = vi.fn()
    render(<Composer convId="c1" onStarted={vi.fn()} onAppended={onAppended}  />)

    const textarea = screen.getByPlaceholderText(/继续提问/)
    await userEvent.type(textarea, '继续扫')
    await userEvent.click(screen.getByRole('button', { name: '发送' }))

    // 发送过程中，SSE 把 lastSeq 推高（模拟并发到达的新事件）。
    useConversationStore.getState().ingest({
      seq: 9,
      id: 'm2',
      conversation_id: 'c1',
      role: 'tool',
      kind: 'event',
      content: '',
      metadata: null,
      created_at: '',
    })

    resolveFollowUp({ intent: 'scan' })
    await waitFor(() => {
      expect(onAppended).toHaveBeenCalledWith(3) // 快照值，不是 9
    })
  })

  it('followUp 失败时显示发送失败提示（busy 裁决已异步化，无同步特例）', async () => {
    vi.mocked(followUp).mockRejectedValue(new Error('network'))
    render(<Composer convId="c1" onStarted={vi.fn()} onAppended={vi.fn()}  />)

    const textarea = screen.getByPlaceholderText(/继续提问/)
    await userEvent.type(textarea, '继续扫')
    await userEvent.click(screen.getByRole('button', { name: '发送' }))

    await waitFor(() => {
      expect(screen.getByText('发送失败')).toBeTruthy()
    })
  })

  it('附件上传中按 Enter 不发送（与发送按钮同一闸门），上传完成后才可发送', async () => {
    let finish!: (v: { name: string; size: number; path: string }) => void
    vi.mocked(uploadAttachment).mockReturnValue(new Promise((r) => (finish = r)))
    vi.mocked(followUp).mockResolvedValue({ intent: 'qa' })
    render(<Composer convId="c1" onStarted={vi.fn()} onAppended={vi.fn()} />)

    fireEvent.change(screen.getByLabelText('选择附件文件'), { target: { files: [new File(['x'], 'a.txt')] } })
    const textarea = screen.getByPlaceholderText(/继续提问/)
    await userEvent.type(textarea, '看看这个{Enter}')
    expect(followUp).not.toHaveBeenCalled()

    finish({ name: 'a.txt', size: 1, path: '.staging/c/a.txt' })
    await waitFor(() => expect(screen.getByText('a.txt')).toBeTruthy())
    await userEvent.type(textarea, '{Enter}')
    await waitFor(() =>
      expect(followUp).toHaveBeenCalledWith('c1', '看看这个', [{ name: 'a.txt', size: 1, path: '.staging/c/a.txt' }]),
    )
  })

  it('Enter 触发发送', async () => {
    vi.mocked(startChat).mockResolvedValue({ conversation_id: 'new-conv', task_id: 't-new' })
    const onStarted = vi.fn()
    render(<Composer onStarted={onStarted} onAppended={vi.fn()}  />)

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
    render(<Composer onStarted={onStarted} onAppended={vi.fn()}  />)

    const textarea = screen.getByPlaceholderText(/描述要扫的目标/) as HTMLTextAreaElement
    await userEvent.type(textarea, '第一行')
    await userEvent.keyboard('{Shift>}{Enter}{/Shift}')
    await userEvent.type(textarea, '第二行')

    expect(startChat).not.toHaveBeenCalled()
    expect(textarea.value).toBe('第一行\n第二行')
  })

  it('空 brief 不发送', async () => {
    render(<Composer onStarted={vi.fn()} onAppended={vi.fn()}  />)
    const sendBtn = screen.getByRole('button', { name: '发送' })
    expect(sendBtn).toBeDisabled()
    await userEvent.click(sendBtn)
    expect(startChat).not.toHaveBeenCalled()
  })
})

describe('Composer 附件流', () => {
  beforeEach(() => {
    useConversationStore.getState().reset()
    vi.mocked(followUp).mockReset()
    vi.mocked(startChat).mockReset()
    vi.mocked(uploadAttachment).mockReset()
  })

  it('选择即上传落芯片，随消息发送；纯附件（无文本）也可发送', async () => {
    vi.mocked(uploadAttachment).mockResolvedValue({
      name: 'targets.txt', size: 12, path: '.staging/pending/u1/targets.txt',
    })
    vi.mocked(startChat).mockResolvedValue({ conversation_id: 'new-conv', task_id: 't-new' })
    const onStarted = vi.fn()
    render(<Composer onStarted={onStarted} onAppended={vi.fn()} />)

    const input = screen.getByLabelText('选择附件文件') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['192.168.1.1'], 'targets.txt')] } })

    // 芯片出现（名称+尺寸），上传请求带文件
    await waitFor(() => {
      expect(uploadAttachment).toHaveBeenCalledTimes(1)
      expect(screen.getByText('targets.txt')).toBeTruthy()
    })

    // 空文本 + 有附件：发送按钮可用，引用随 startChat 提交后清空
    const sendBtn = screen.getByRole('button', { name: '发送' })
    expect(sendBtn).not.toBeDisabled()
    await userEvent.click(sendBtn)
    await waitFor(() => {
      expect(startChat).toHaveBeenCalledWith('', [
        { name: 'targets.txt', size: 12, path: '.staging/pending/u1/targets.txt' },
      ])
      expect(onStarted).toHaveBeenCalledWith('new-conv')
    })
    expect(screen.queryByText('targets.txt')).toBeNull()
  })

  it('上传失败就近提示，不中断输入', async () => {
    vi.mocked(uploadAttachment).mockRejectedValue(new Error('上传失败 413: 附件超过上限 100 MiB'))
    render(<Composer convId="c1" onStarted={vi.fn()} onAppended={vi.fn()} />)

    const input = screen.getByLabelText('选择附件文件') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['x'], 'big.bin')] } })

    await waitFor(() => {
      expect(screen.getByText(/附件超过上限/)).toBeTruthy()
    })
    // 失败后输入与发送不受影响
    expect(screen.getByRole('button', { name: '发送' })).toBeDisabled() // 无文本无附件
  })

  it('芯片可移除，移除后不随消息发送', async () => {
    vi.mocked(uploadAttachment).mockResolvedValue({
      name: 'a.txt', size: 1, path: '.staging/conv/c1/a.txt',
    })
    render(<Composer convId="c1" onStarted={vi.fn()} onAppended={vi.fn()} />)

    const input = screen.getByLabelText('选择附件文件') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['x'], 'a.txt')] } })
    await waitFor(() => expect(screen.getByText('a.txt')).toBeTruthy())

    await userEvent.click(screen.getByRole('button', { name: '移除附件 a.txt' }))
    expect(screen.queryByText('a.txt')).toBeNull()
  })
})
describe('Composer 跨会话', () => {
  beforeEach(() => {
    useConversationStore.getState().reset()
    vi.mocked(followUp).mockReset()
    vi.mocked(startChat).mockReset()
    vi.mocked(uploadAttachment).mockReset()
  })

  it('切换会话清空附件芯片（引用是会话域资源，不跨会话提交）', async () => {
    vi.mocked(uploadAttachment).mockResolvedValue({
      name: 'a.txt', size: 1, path: '.staging/conv/c1/a.txt',
    })
    const { rerender } = render(<Composer convId="c1" onStarted={vi.fn()} onAppended={vi.fn()} />)
    const input = screen.getByLabelText('选择附件文件') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['x'], 'a.txt')] } })
    await waitFor(() => expect(screen.getByText('a.txt')).toBeTruthy())

    rerender(<Composer convId="c2" onStarted={vi.fn()} onAppended={vi.fn()} />)
    expect(screen.queryByText('a.txt')).toBeNull()
  })
})

