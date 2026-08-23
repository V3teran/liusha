import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { ConversationsPage } from './ConversationsPage'

// ConversationsPage 只做主从双栏编排（当前选中会话 + 列表刷新 ref + 删除态清理），
// 子组件内部实现（SSE、消息渲染等）由各自测试覆盖——这里用 stub 暴露关心的 props。
const listProps = vi.fn()
const detailProps = vi.fn()

vi.mock('@/features/conversation/ConversationList', () => ({
  ConversationList: (props: Record<string, unknown>) => {
    listProps(props)
    return (
      <div data-testid="conv-list">
        <button type="button" onClick={() => (props.onSelect as (id: string) => void)('conv-selected')}>
          select
        </button>
        <button type="button" onClick={() => (props.onNew as () => void)()}>
          new
        </button>
        <button type="button" onClick={() => (props.onDeleted as (id: string) => void)('conv-selected')}>
          delete-selected
        </button>
        <button type="button" onClick={() => (props.onDeleted as (id: string) => void)('conv-other')}>
          delete-other
        </button>
      </div>
    )
  },
}))

vi.mock('@/features/conversation/ConversationDetail', () => ({
  ConversationDetail: (props: Record<string, unknown>) => {
    detailProps(props)
    return (
      <div data-testid="conv-detail">
        <button type="button" onClick={() => (props.onStarted as (id: string) => void)('conv-new-started')}>
          start
        </button>
      </div>
    )
  },
}))

function renderPage(source: 'manual' | 'auto', initialPath = '/') {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <ConversationsPage source={source} />
    </MemoryRouter>,
  )
}

describe('ConversationsPage', () => {
  it('主动下发(manual) 下读取 ?conv= 并作为初始会话传给 ConversationDetail', () => {
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('manual', '/?conv=conv-from-query')

    const lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBe('conv-from-query')
  })

  it('被动代理(auto) 下不读取 ?conv=（仅 manual 生效）', () => {
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('auto', '/?conv=conv-from-query')

    const lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBeUndefined()
  })

  it('onStarted 设置当前会话并传给 ConversationList 的 activeId', async () => {
    const user = userEvent.setup()
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('manual')

    await user.click(screen.getByText('start'))

    const lastListCall = listProps.mock.calls[listProps.mock.calls.length - 1][0]
    expect(lastListCall.activeId).toBe('conv-new-started')
  })

  it('onConvDeleted：删除的是当前打开会话时清空 currentConv', async () => {
    const user = userEvent.setup()
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('manual')

    // 先选中 conv-selected
    await user.click(screen.getByText('select'))
    let lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBe('conv-selected')

    // 删除同一会话 → 清空
    await user.click(screen.getByText('delete-selected'))
    lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBeUndefined()
  })

  it('onConvDeleted：删除的不是当前打开会话时不影响 currentConv', async () => {
    const user = userEvent.setup()
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('manual')

    await user.click(screen.getByText('select'))
    await user.click(screen.getByText('delete-other'))

    const lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBe('conv-selected')
  })

  it('主动下发(manual)：allowNew=true，无自定义 heading/emptyHint', () => {
    listProps.mockClear()
    renderPage('manual')
    const lastListCall = listProps.mock.calls[listProps.mock.calls.length - 1][0]
    expect(lastListCall.allowNew).toBe(true)
    expect(lastListCall.heading).toBeUndefined()
    expect(lastListCall.emptyHint).toBeUndefined()
  })

  it('被动代理(auto)：allowNew=false，heading=流量批次，emptyHint 有值', () => {
    listProps.mockClear()
    renderPage('auto')
    const lastListCall = listProps.mock.calls[listProps.mock.calls.length - 1][0]
    expect(lastListCall.allowNew).toBe(false)
    expect(lastListCall.heading).toBe('流量批次')
    expect(lastListCall.emptyHint).toMatch(/流量/)
  })

  it('onNew：新建会话时清空 currentConv（新建态）', async () => {
    const user = userEvent.setup()
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('manual')

    await user.click(screen.getByText('select')) // 先有个选中
    await user.click(screen.getByText('new')) // 再新建 → 清空

    const lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBeUndefined()
  })

  it('source 透传给 ConversationList 与 ConversationDetail', () => {
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('auto')
    expect(listProps.mock.calls[listProps.mock.calls.length - 1][0].source).toBe('auto')
    expect(detailProps.mock.calls[detailProps.mock.calls.length - 1][0].source).toBe('auto')
  })
})
