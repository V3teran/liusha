import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'
import { SystemConfigPage } from './SystemConfigPage'
import { getSettings, patchSettings, resetSettings } from '@/api/settings'
import type {
  CompactionSettings,
  RuntimeSettings,
  ProxyFilterSettings,
  SettingsDetail,
  SettingsGroup,
} from '@/api/types'

vi.mock('@/api/settings', () => ({
  getSettings: vi.fn(),
  patchSettings: vi.fn(),
  resetSettings: vi.fn(),
}))

const mGet = getSettings as unknown as ReturnType<typeof vi.fn>
const mPatch = patchSettings as unknown as ReturnType<typeof vi.fn>
const mReset = resetSettings as unknown as ReturnType<typeof vi.fn>

const compaction: CompactionSettings = {
  planner_context_ratio: 0.6,
  executor_context_ratio: 0.5,
  evaluator_context_ratio: 0.5,
  monitor_context_ratio: 0.5,
}
const runtime: RuntimeSettings = {
  step_tool_timeout_seconds: 60,
  step_llm_timeout_seconds: 300,
  run_tail_bytes: 4096,
  tool_output_tail_bytes: 32768,
}
const proxy: ProxyFilterSettings = {
  allow_hosts: ['*.target.com'],
  exclude_methods: ['CONNECT'],
  exclude_hosts: [],
  exclude_upgrade_protocols: [],
  exclude_suffixes: ['.css'],
  exclude_content_types: [],
  exclude_status_codes: [304],
  max_request_body_size: 1048576,
  max_response_body_size: 2097152,
}

const plain = <T,>(values: T): SettingsDetail<T> => ({ values, defaults: values, overrides: {} })
const byGroup: Record<SettingsGroup, SettingsDetail<unknown>> = {
  compaction: plain(compaction),
  runtime: plain(runtime),
  proxy_filter: plain(proxy),
}

// RuntimeSection 保存后失效 React Query 缓存（useQueryClient）——测试壳须提供 Provider。
function renderWithQueryClient(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

describe('SystemConfigPage', () => {
  beforeEach(() => {
    mGet.mockReset().mockImplementation((g: SettingsGroup) => Promise.resolve(byGroup[g]))
    mPatch.mockReset().mockImplementation((g: SettingsGroup) => Promise.resolve(byGroup[g]))
    mReset.mockReset()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('三组旋钮各自加载并渲染当前值', async () => {
    renderWithQueryClient(<SystemConfigPage />)
    // 三个分区标题
    expect(await screen.findByText('上下文压缩')).toBeInTheDocument()
    expect(screen.getByText('工具运行时')).toBeInTheDocument()
    expect(screen.getByText('代理流量过滤规则')).toBeInTheDocument()
    // compaction 占比回填
    await waitFor(() => expect(screen.getByDisplayValue('0.6')).toBeInTheDocument())
    // proxy 白名单以多行文本回填
    expect(screen.getByDisplayValue('*.target.com')).toBeInTheDocument()
  })

  it('编辑 compaction 后保存：只提交改动字段', async () => {
    const user = userEvent.setup()
    renderWithQueryClient(<SystemConfigPage />)
    const input = await screen.findByDisplayValue('0.6') // planner_context_ratio
    await user.clear(input)
    await user.type(input, '0.7')

    const saveButtons = screen.getAllByRole('button', { name: '保存' })
    await user.click(saveButtons[0]) // 第一区 = compaction

    await waitFor(() => expect(mPatch).toHaveBeenCalledTimes(1))
    expect(mPatch.mock.calls[0]).toEqual(['compaction', { planner_context_ratio: 0.7 }])
    expect(await screen.findByText('已保存并生效')).toBeInTheDocument()
  })

  it('名单字段保存为 {on, off} 操作', async () => {
    const user = userEvent.setup()
    renderWithQueryClient(<SystemConfigPage />)
    const allow = await screen.findByDisplayValue('*.target.com')
    await user.clear(allow)
    await user.type(allow, 'app.example')
    await user.click(screen.getAllByRole('button', { name: '保存' })[2])
    await waitFor(() => expect(mPatch).toHaveBeenCalledTimes(1))
    expect(mPatch.mock.calls[0]).toEqual(['proxy_filter', { allow_hosts: { on: ['app.example'], off: ['*.target.com'] } }])
  })

  it('已自定义字段显示状态条，可单项恢复出厂与整组恢复', async () => {
    const user = userEvent.setup()
    const customized: SettingsDetail<RuntimeSettings> = {
      values: { ...runtime, step_llm_timeout_seconds: 120 },
      defaults: runtime,
      overrides: {
        step_llm_timeout_seconds: { kind: 'replace', default: 300, baseline: 300, default_changed: false, matches_default: false },
      },
    }
    mGet.mockImplementation((g: SettingsGroup) => Promise.resolve(g === 'runtime' ? customized : byGroup[g]))
    mReset.mockResolvedValue(byGroup.runtime)
    renderWithQueryClient(<SystemConfigPage />)
    await user.click(await screen.findByText('恢复默认'))
    await waitFor(() => expect(mReset).toHaveBeenCalledWith('runtime', 'step_llm_timeout_seconds'))
    await waitFor(() => expect(screen.queryByText('已自定义')).toBeNull())
    expect(screen.getByDisplayValue('300')).toBeInTheDocument()
  })

  it('proxy body 上限清零 → 该区保存按钮禁用', async () => {
    const user = userEvent.setup()
    renderWithQueryClient(<SystemConfigPage />)
    const reqBody = await screen.findByDisplayValue('1048576')
    await user.clear(reqBody) // 空 → Number('') = 0 → invalid

    const saveButtons = screen.getAllByRole('button', { name: '保存' })
    // 第三区 = proxy_filter
    expect(saveButtons[2]).toBeDisabled()
  })

  // 搜索只是筛选显示：被筛掉的分区不得卸载，否则其中未保存的编辑静默丢失。
  it('搜索筛掉分区再清空，未保存的编辑仍在', async () => {
    const user = userEvent.setup()
    renderWithQueryClient(<SystemConfigPage />)
    const timeout = await screen.findByDisplayValue('60')
    await user.clear(timeout)
    await user.type(timeout, '120')
    const search = screen.getByPlaceholderText('搜索配置项')
    await user.type(search, '代理')
    await user.clear(search)
    expect(screen.getByDisplayValue('120')).toBeInTheDocument()
  })

  // 保存请求在途时继续编辑另一字段：响应只更新已提交的字段，新输入不被覆盖。
  it('保存在途时的新编辑不被响应覆盖', async () => {
    const user = userEvent.setup()
    let resolve!: (v: SettingsDetail<unknown>) => void
    mPatch.mockImplementation(() => new Promise((r) => (resolve = r)))
    renderWithQueryClient(<SystemConfigPage />)
    const planner = await screen.findByDisplayValue('0.6')
    await user.clear(planner)
    await user.type(planner, '0.7')
    await user.click(screen.getAllByRole('button', { name: '保存' })[0])
    const executor = screen.getAllByDisplayValue('0.5')[0]
    await user.clear(executor)
    await user.type(executor, '0.8')
    resolve(plain({ ...compaction, planner_context_ratio: 0.7 }))
    await waitFor(() => expect(screen.getByDisplayValue('0.7')).toBeInTheDocument())
    expect(screen.getByDisplayValue('0.8')).toBeInTheDocument()
  })

  it('搜索"压缩"仅显示上下文压缩分区', async () => {
    const user = userEvent.setup()
    renderWithQueryClient(<SystemConfigPage />)

    // 等待所有分区加载
    await screen.findByText('上下文压缩')
    expect(screen.getByText('工具运行时')).toBeInTheDocument()
    expect(screen.getByText('代理流量过滤规则')).toBeInTheDocument()

    // 输入搜索关键词
    const searchInput = screen.getByPlaceholderText('搜索配置项')
    await user.type(searchInput, '压缩')

    // 只显示匹配的分区
    expect(screen.getByText('上下文压缩')).toBeInTheDocument()
    expect(screen.getByText('工具运行时')).not.toBeVisible()
    expect(screen.getByText('代理流量过滤规则')).not.toBeVisible()
  })

  it('搜索"流量"仅显示代理流量过滤规则分区', async () => {
    const user = userEvent.setup()
    renderWithQueryClient(<SystemConfigPage />)

    await screen.findByText('上下文压缩')

    const searchInput = screen.getByPlaceholderText('搜索配置项')
    await user.type(searchInput, '流量')

    expect(screen.getByText('上下文压缩')).not.toBeVisible()
    expect(screen.getByText('工具运行时')).not.toBeVisible()
    expect(screen.getByText('代理流量过滤规则')).toBeVisible()
  })

  it('搜索不存在的关键词显示空状态', async () => {
    const user = userEvent.setup()
    renderWithQueryClient(<SystemConfigPage />)

    await screen.findByText('上下文压缩')

    const searchInput = screen.getByPlaceholderText('搜索配置项')
    await user.type(searchInput, '不存在的配置项xyz')

    expect(screen.getByText('上下文压缩')).not.toBeVisible()
    expect(screen.getByText('工具运行时')).not.toBeVisible()
    expect(screen.getByText('代理流量过滤规则')).not.toBeVisible()
    expect(screen.getByText('未找到匹配的配置项')).toBeVisible()
  })

  it('清空搜索框恢复显示所有分区', async () => {
    const user = userEvent.setup()
    renderWithQueryClient(<SystemConfigPage />)

    await screen.findByText('上下文压缩')

    const searchInput = screen.getByPlaceholderText('搜索配置项')
    await user.type(searchInput, '压缩')

    // 只显示一个分区
    expect(screen.getByText('上下文压缩')).toBeVisible()
    expect(screen.getByText('工具运行时')).not.toBeVisible()

    // 清空搜索
    await user.clear(searchInput)

    // 恢复显示所有分区
    expect(screen.getByText('上下文压缩')).toBeVisible()
    expect(screen.getByText('工具运行时')).toBeVisible()
    expect(screen.getByText('代理流量过滤规则')).toBeVisible()
  })
})
