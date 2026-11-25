import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
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
        <button
          type="button"
          onClick={() => (props.onSelect as (id: string, o: { replace: boolean }) => void)('conv-auto', { replace: true })}
        >
          auto-select
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

// 「代理接入」自带状态查询，与本页编排无关
vi.mock('@/features/proxy/ProxyAccess', () => ({ ProxyAccess: () => <button type="button">代理接入</button> }))

vi.mock('@/features/conversation/ConversationDetail', () => ({
  ConversationDetail: (props: Record<string, unknown>) => {
    detailProps(props)
    return (
      <div data-testid="conv-detail">
        {/* 页面交下来的顶栏前置控件（列表的收起 / 展开开关）：真实组件只在有顶栏（打开了对话）时
            把它放在顶栏最前面 */}
        {props.convId ? (props.leading as ReactNode) : null}
        <button type="button" onClick={() => (props.onStarted as (id: string) => void)('conv-new-started')}>
          start
        </button>
        <button type="button" onClick={() => (props.onAgentsChange as (names: string[]) => void)(['executor', 'planner'])}>
          filter-two
        </button>
      </div>
    )
  },
}))

function LocationProbe() {
  const navigate = useNavigate()
  return (
    <>
      <output data-testid="location">{useLocation().search}</output>
      <button type="button" onClick={() => navigate(-1)}>
        浏览器后退
      </button>
    </>
  )
}

// initialPath 之前垫一条别的页面，模拟从别处进入对话页（后退离开对话页时回到它）
function renderPage(source: 'direct' | 'proxy', initialPath = '/') {
  return render(
    <MemoryRouter initialEntries={['/?from=elsewhere', initialPath]} initialIndex={1}>
      <ConversationsPage source={source} />
      <LocationProbe />
    </MemoryRouter>,
  )
}

describe('ConversationsPage', () => {
  it('人工下发(direct) 下读取 ?conversation= 并作为初始会话传给 ConversationDetail', () => {
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('direct', '/?conversation=conv-from-query')

    const lastDetailCall = detailProps.mock.calls[detailProps.mock.calls.length - 1][0]
    expect(lastDetailCall.convId).toBe('conv-from-query')
  })

  it('代理捕获(proxy) 同样读取 ?conversation=（深链双 tab 生效）', () => {
    listProps.mockClear()
    detailProps.mockClear()
    renderPage('proxy', '/?conversation=conv-from-query')

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

  it('人工下发(direct)：allowNew=true，空态名词为对话', () => {
    listProps.mockClear()
    renderPage('direct')
    const lastListCall = listProps.mock.calls[listProps.mock.calls.length - 1][0]
    expect(lastListCall.allowNew).toBe(true)
    expect(lastListCall.emptyState.noun).toBe('对话')
  })

  it('代理捕获(proxy)：allowNew=false，空态引导挂代理', () => {
    listProps.mockClear()
    renderPage('proxy')
    const lastListCall = listProps.mock.calls[listProps.mock.calls.length - 1][0]
    expect(lastListCall.allowNew).toBe(false)
    expect(lastListCall.emptyState.noun).toBe('代理批次')
    expect(lastListCall.emptyState.hint).toMatch(/代理接入/)
  })

  // 「代理接入」只在代理捕获出现：人工下发与代理无关
  it('代理捕获页头带「代理接入」，人工下发不带', () => {
    const { unmount } = renderPage('proxy')
    expect(screen.getByRole('button', { name: '代理接入' })).toBeInTheDocument()
    unmount()
    renderPage('direct')
    expect(screen.queryByRole('button', { name: '代理接入' })).toBeNull()
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
  it('从深链打开 A 后选择 B：URL 随之变为 ?conversation=B（刷新/分享还原 B）', async () => {
    const user = userEvent.setup()
    renderPage('direct', '/?conversation=conv-a')
    await user.click(screen.getByText('select'))
    expect(screen.getByTestId('location').textContent).toBe('?conversation=conv-selected')
    expect(detailProps.mock.calls.at(-1)?.[0].convId).toBe('conv-selected')
  })

  // 用户点开、切换对话是一步导航：新增历史，后退回到上一个对话
  it('依次点开两个对话，后退回到上一个', async () => {
    const user = userEvent.setup()
    renderPage('direct', '/?conversation=conv-a')
    await user.click(screen.getByText('select'))
    await user.click(screen.getByText('浏览器后退'))
    expect(screen.getByTestId('location').textContent).toBe('?conversation=conv-a')
  })

  // 进页自动打开最新对话不进历史：后退直接离开对话页，而不是停在「没有选中」
  it('自动打开最新对话改写当前历史', async () => {
    const user = userEvent.setup()
    renderPage('direct')
    await user.click(screen.getByText('auto-select'))
    expect(screen.getByTestId('location').textContent).toBe('?conversation=conv-auto')
    await user.click(screen.getByText('浏览器后退'))
    expect(screen.getByTestId('location').textContent).toBe('?from=elsewhere')
  })

  it('新建：清除 URL 中的会话，详情回到新建态', async () => {
    const user = userEvent.setup()
    renderPage('direct', '/?conversation=conv-a')
    await user.click(screen.getByText('new'))
    expect(screen.getByTestId('location').textContent).toBe('')
    expect(detailProps.mock.calls.at(-1)?.[0].convId).toBeUndefined()
  })
})

// 按视口宽度模拟 matchMedia（只解析 min-width 断点）。
function stubViewport(width: number) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn((q: string) => ({
      matches: Number(/min-width:\s*(\d+)px/.exec(q)?.[1] ?? 0) <= width,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  )
}

describe('ConversationsPage 窄屏（< 768）主从单栏', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('无选中会话时只显示列表，且不自动选中（否则一进页面就被推进详情）', () => {
    stubViewport(390)
    listProps.mockClear()
    renderPage('direct')
    expect(screen.getByTestId('conv-list')).toBeInTheDocument()
    expect(screen.queryByTestId('conv-detail')).toBeNull()
    expect(listProps.mock.calls.at(-1)?.[0].autoSelect).toBe(false)
  })

  it('点选会话进入详情，「返回对话列表」回到列表，地址栏随之清除对话', async () => {
    stubViewport(390)
    const user = userEvent.setup()
    renderPage('direct')
    await user.click(screen.getByText('select'))
    expect(screen.getByTestId('conv-detail')).toBeInTheDocument()
    expect(screen.queryByTestId('conv-list')).toBeNull()
    await user.click(screen.getByRole('button', { name: '返回对话列表' }))
    expect(screen.getByTestId('conv-list')).toBeInTheDocument()
    expect(screen.queryByTestId('conv-detail')).toBeNull()
    expect(screen.getByTestId('location').textContent).toBe('')
  })

  // 窄屏的「详情 / 列表」由 URL 决定：浏览器后退与「返回对话列表」结果一致
  it('点选会话进入详情，浏览器后退回到列表（而不是离开页面）', async () => {
    stubViewport(390)
    const user = userEvent.setup()
    renderPage('direct')
    await user.click(screen.getByText('select'))
    await user.click(screen.getByText('浏览器后退'))
    expect(screen.getByTestId('conv-list')).toBeInTheDocument()
    expect(screen.queryByTestId('conv-detail')).toBeNull()
  })

  it('新对话（还没有对话 ID）进入输入框，浏览器后退回到列表', async () => {
    stubViewport(390)
    const user = userEvent.setup()
    renderPage('direct')
    await user.click(screen.getByText('new'))
    expect(screen.getByTestId('conv-detail')).toBeInTheDocument()
    await user.click(screen.getByText('浏览器后退'))
    expect(screen.getByTestId('conv-list')).toBeInTheDocument()
  })

  it('深链带 ?conversation= 直接落在详情', () => {
    stubViewport(390)
    renderPage('direct', '/?conversation=conv-a')
    expect(screen.getByTestId('conv-detail')).toBeInTheDocument()
    expect(screen.queryByTestId('conv-list')).toBeNull()
  })

  it('新对话进入详情（新建态）', async () => {
    stubViewport(390)
    const user = userEvent.setup()
    renderPage('direct')
    await user.click(screen.getByText('new'))
    expect(screen.getByTestId('conv-detail')).toBeInTheDocument()
  })

  it('宽屏双栏并存，列表自动选中首项', () => {
    stubViewport(1440)
    listProps.mockClear()
    renderPage('direct')
    expect(screen.getByTestId('conv-list')).toBeInTheDocument()
    expect(screen.getByTestId('conv-detail')).toBeInTheDocument()
    expect(listProps.mock.calls.at(-1)?.[0].autoSelect).toBe(true)
    expect(screen.queryByRole('button', { name: '返回对话列表' })).toBeNull()
  })
})

const COLLAPSED_KEY = 'liusha-conversation-list-collapsed'

// 列表可以向左收起：开关固定在详情这一栏的左上角（与侧栏的收起开关同款），收起后对话占满。
// 这是「怎么看」而不是「打开了什么」，所以记在本地、不进地址栏。
describe('ConversationsPage 宽屏：列表的收起与展开', () => {
  afterEach(() => localStorage.removeItem(COLLAPSED_KEY))

  it('顶栏最前面的开关把列表向左收起，再点展开；偏好记在本地', async () => {
    const user = userEvent.setup()
    renderPage('direct', '/conversations/direct?conversation=c-1')
    const toggle = screen.getByRole('button', { name: '收起对话列表' })
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(toggle.getAttribute('aria-controls')).toBe('conversation-list-pane')
    expect(document.getElementById('conversation-list-pane')).toBeTruthy()

    await user.click(toggle)
    expect(screen.queryByTestId('conv-list')).toBeNull()
    const reopen = screen.getByRole('button', { name: '展开对话列表' })
    expect(reopen.getAttribute('aria-expanded')).toBe('false')
    expect(localStorage.getItem(COLLAPSED_KEY)).toBe('1')
    // 收起不改变打开的对话
    expect(screen.getByTestId('location').textContent).toBe('?conversation=c-1')

    await user.click(reopen)
    expect(screen.getByTestId('conv-list')).toBeTruthy()
    expect(localStorage.getItem(COLLAPSED_KEY)).toBe('0')
  })

  it('重新进入时保持上次的取舍', () => {
    localStorage.setItem(COLLAPSED_KEY, '1')
    renderPage('direct', '/conversations/direct?conversation=c-1')
    expect(screen.queryByTestId('conv-list')).toBeNull()
    expect(screen.getByRole('button', { name: '展开对话列表' })).toBeTruthy()
  })

  it('没有打开的对话（没有顶栏）时开关照样在', () => {
    renderPage('direct', '/conversations/direct')
    expect(screen.getByRole('button', { name: '收起对话列表' })).toBeTruthy()
  })
})

// 按智能体筛选可以多选，记在地址栏上（每个选中的智能体一个 agent 参数）；筛选属于它所在的那个对话。
describe('ConversationsPage 多选筛选与地址栏', () => {
  it('从地址栏读出选中的几个智能体交给详情', () => {
    renderPage('direct', '/conversations/direct?conversation=c-1&agent=executor&agent=planner')
    expect(detailProps.mock.lastCall?.[0].agents).toEqual(['executor', 'planner'])
  })

  it('详情交回几个名字，地址栏就写几个 agent 参数（追加，不是替换）', async () => {
    renderPage('direct', '/conversations/direct?conversation=c-1&agent=executor')
    await waitFor(() => expect(detailProps).toHaveBeenCalled())
    act(() => {
      screen.getByRole('button', { name: 'filter-two' }).click()
    })
    const search = new URLSearchParams(screen.getByTestId('location').textContent ?? '')
    expect(search.getAll('agent')).toEqual(['executor', 'planner'])
    expect(detailProps.mock.lastCall?.[0].agents).toEqual(['executor', 'planner'])
  })

  it('换一个对话即清除筛选', async () => {
    const user = userEvent.setup()
    renderPage('direct', '/conversations/direct?conversation=c-1&agent=executor&agent=planner')
    await user.click(screen.getByRole('button', { name: 'select' }))
    expect(screen.getByTestId('location').textContent).not.toContain('agent=')
  })
})
