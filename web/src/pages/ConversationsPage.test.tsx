import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router-dom'
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

function LocationProbe() {
  return <output data-testid="location">{useLocation().search}</output>
}

function renderPage(source: 'direct' | 'proxy', initialPath = '/') {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <ConversationsPage source={source} />
      <LocationProbe />
    </MemoryRouter>,
  )
}

describe('ConversationsPage', () => {
  it('人工下发(direct) 下读取 ?conv= 并作为初始会话传给 ConversationDetail', () => {
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('direct', '/?conv=conv-from-query')

    const lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBe('conv-from-query')
  })

  it('代理捕获(proxy) 同样读取 ?conv=（深链双 tab 生效）', () => {
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('proxy', '/?conv=conv-from-query')

    const lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBe('conv-from-query')
  })

  it('onStarted 设置当前会话并传给 ConversationList 的 activeId', async () => {
    const user = userEvent.setup()
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('direct')

    await user.click(screen.getByText('start'))

    const lastListCall = listProps.mock.calls[listProps.mock.calls.length - 1][0]
    expect(lastListCall.activeId).toBe('conv-new-started')
  })

  it('onConvDeleted：删除的是当前打开会话时清空 currentConv', async () => {
    const user = userEvent.setup()
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('direct')

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
    renderPage('direct')

    await user.click(screen.getByText('select'))
    await user.click(screen.getByText('delete-other'))

    const lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBe('conv-selected')
  })

  it('人工下发(direct)：allowNew=true，无自定义 heading，带结构化空态', () => {
    listProps.mockClear()
    renderPage('direct')
    const lastListCall = listProps.mock.calls[listProps.mock.calls.length - 1][0]
    expect(lastListCall.allowNew).toBe(true)
    expect(lastListCall.heading).toBeUndefined()
    expect(lastListCall.emptyState.title).toBe('暂无人工下发的对话')
  })

  it('代理捕获(proxy)：allowNew=false，heading=代理批次，空态引导挂代理', () => {
    listProps.mockClear()
    renderPage('proxy')
    const lastListCall = listProps.mock.calls[listProps.mock.calls.length - 1][0]
    expect(lastListCall.allowNew).toBe(false)
    expect(lastListCall.heading).toBe('代理批次')
    expect(lastListCall.emptyState.desc).toMatch(/8588/)
  })

  it('onNew：新建会话时清空 currentConv（新建态）', async () => {
    const user = userEvent.setup()
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('direct')

    await user.click(screen.getByText('select')) // 先有个选中
    await user.click(screen.getByText('new')) // 再新建 → 清空

    const lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBeUndefined()
  })

  it('source 透传给 ConversationList 与 ConversationDetail', () => {
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('proxy')
    expect(listProps.mock.calls[listProps.mock.calls.length - 1][0].source).toBe('proxy')
    expect(detailProps.mock.calls[detailProps.mock.calls.length - 1][0].source).toBe('proxy')
  })
})

describe('ConversationsPage URL 状态', () => {
  it('从深链打开 A 后选择 B：URL 随之变为 ?conv=B（刷新/分享还原 B）', async () => {
    const user = userEvent.setup()
    renderPage('direct', '/?conv=conv-a')
    await user.click(screen.getByText('select'))
    expect(screen.getByTestId('location').textContent).toBe('?conv=conv-selected')
    expect(detailProps.mock.calls.at(-1)?.[0].convId).toBe('conv-selected')
  })

  it('新建：清除 URL 中的会话，详情回到新建态', async () => {
    const user = userEvent.setup()
    renderPage('direct', '/?conv=conv-a')
    await user.click(screen.getByText('new'))
    expect(screen.getByTestId('location').textContent).toBe('')
    expect(detailProps.mock.calls.at(-1)?.[0].convId).toBeUndefined()
  })
})
