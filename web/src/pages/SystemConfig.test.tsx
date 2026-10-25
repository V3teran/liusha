import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SystemConfig } from './SystemConfig'
import {
  getCompactionSettings,
  saveCompactionSettings,
  getRuntimeSettings,
  saveRuntimeSettings,
  getProxyFilterSettings,
  saveProxyFilterSettings,
} from '@/api/settings'
import type {
  CompactionSettings,
  RuntimeSettings,
  ProxyFilterSettings,
} from '@/api/types'

vi.mock('@/api/settings', () => ({
  getCompactionSettings: vi.fn(),
  saveCompactionSettings: vi.fn(),
  getRuntimeSettings: vi.fn(),
  saveRuntimeSettings: vi.fn(),
  getProxyFilterSettings: vi.fn(),
  saveProxyFilterSettings: vi.fn(),
}))

const mGetCompaction = getCompactionSettings as unknown as ReturnType<typeof vi.fn>
const mSaveCompaction = saveCompactionSettings as unknown as ReturnType<typeof vi.fn>
const mGetRuntime = getRuntimeSettings as unknown as ReturnType<typeof vi.fn>
const mSaveRuntime = saveRuntimeSettings as unknown as ReturnType<typeof vi.fn>
const mGetProxy = getProxyFilterSettings as unknown as ReturnType<typeof vi.fn>
const mSaveProxy = saveProxyFilterSettings as unknown as ReturnType<typeof vi.fn>

const compaction: CompactionSettings = {
  trigger_ratio: 0.8,
  trailing_budget_ratio: 0.5,
  compactor_timeout_seconds: 30,
}
const runtime: RuntimeSettings = {
  step_tool_timeout_seconds: 60,
  run_tail_bytes: 4096,
  findings_limit_in_prompt: 100,
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

describe('SystemConfig', () => {
  beforeEach(() => {
    mGetCompaction.mockReset().mockResolvedValue(compaction)
    mSaveCompaction.mockReset().mockImplementation((v: CompactionSettings) => Promise.resolve(v))
    mGetRuntime.mockReset().mockResolvedValue(runtime)
    mSaveRuntime.mockReset().mockImplementation((v: RuntimeSettings) => Promise.resolve(v))
    mGetProxy.mockReset().mockResolvedValue(proxy)
    mSaveProxy.mockReset().mockImplementation((v: ProxyFilterSettings) => Promise.resolve(v))
  })

  it('三组旋钮各自加载并渲染当前值', async () => {
    render(<SystemConfig />)
    // 三个分区标题
    expect(await screen.findByText('会话历史压缩')).toBeInTheDocument()
    expect(screen.getByText('工具运行时')).toBeInTheDocument()
    expect(screen.getByText('代理流量过滤规则')).toBeInTheDocument()
    // compaction 触发阈值回填
    await waitFor(() => expect(screen.getByDisplayValue('0.8')).toBeInTheDocument())
    // proxy 白名单以多行文本回填
    expect(screen.getByDisplayValue('*.target.com')).toBeInTheDocument()
  })

  it('编辑 compaction 触发阈值后保存 → 调 saveCompactionSettings 透传新值', async () => {
    const user = userEvent.setup()
    render(<SystemConfig />)
    const input = await screen.findByDisplayValue('30') // compactor_timeout_seconds
    await user.clear(input)
    await user.type(input, '45')

    const saveButtons = screen.getAllByRole('button', { name: '保存' })
    await user.click(saveButtons[0]) // 第一区 = compaction

    await waitFor(() => expect(mSaveCompaction).toHaveBeenCalledTimes(1))
    expect(mSaveCompaction.mock.calls[0][0].compactor_timeout_seconds).toBe(45)
    expect(await screen.findByText('已保存并热生效')).toBeInTheDocument()
  })

  it('proxy body 上限清零 → 该区保存按钮禁用', async () => {
    const user = userEvent.setup()
    render(<SystemConfig />)
    const reqBody = await screen.findByDisplayValue('1048576')
    await user.clear(reqBody) // 空 → Number('') = 0 → invalid

    const saveButtons = screen.getAllByRole('button', { name: '保存' })
    // 第三区 = proxy_filter
    expect(saveButtons[2]).toBeDisabled()
  })

  it('搜索"压缩"仅显示会话历史压缩分区', async () => {
    const user = userEvent.setup()
    render(<SystemConfig />)

    // 等待所有分区加载
    await screen.findByText('会话历史压缩')
    expect(screen.getByText('工具运行时')).toBeInTheDocument()
    expect(screen.getByText('代理流量过滤规则')).toBeInTheDocument()

    // 输入搜索关键词
    const searchInput = screen.getByPlaceholderText('搜索配置项')
    await user.type(searchInput, '压缩')

    // 只显示匹配的分区
    expect(screen.getByText('会话历史压缩')).toBeInTheDocument()
    expect(screen.queryByText('工具运行时')).not.toBeInTheDocument()
    expect(screen.queryByText('代理流量过滤规则')).not.toBeInTheDocument()
  })

  it('搜索"流量"仅显示代理流量过滤规则分区', async () => {
    const user = userEvent.setup()
    render(<SystemConfig />)

    await screen.findByText('会话历史压缩')

    const searchInput = screen.getByPlaceholderText('搜索配置项')
    await user.type(searchInput, '流量')

    expect(screen.queryByText('会话历史压缩')).not.toBeInTheDocument()
    expect(screen.queryByText('工具运行时')).not.toBeInTheDocument()
    expect(screen.getByText('代理流量过滤规则')).toBeInTheDocument()
  })

  it('搜索不存在的关键词显示空状态', async () => {
    const user = userEvent.setup()
    render(<SystemConfig />)

    await screen.findByText('会话历史压缩')

    const searchInput = screen.getByPlaceholderText('搜索配置项')
    await user.type(searchInput, '不存在的配置项xyz')

    expect(screen.queryByText('会话历史压缩')).not.toBeInTheDocument()
    expect(screen.queryByText('工具运行时')).not.toBeInTheDocument()
    expect(screen.queryByText('代理流量过滤规则')).not.toBeInTheDocument()
    expect(screen.getByText('未找到匹配的配置项')).toBeInTheDocument()
  })

  it('清空搜索框恢复显示所有分区', async () => {
    const user = userEvent.setup()
    render(<SystemConfig />)

    await screen.findByText('会话历史压缩')

    const searchInput = screen.getByPlaceholderText('搜索配置项')
    await user.type(searchInput, '压缩')

    // 只显示一个分区
    expect(screen.getByText('会话历史压缩')).toBeInTheDocument()
    expect(screen.queryByText('工具运行时')).not.toBeInTheDocument()

    // 清空搜索
    await user.clear(searchInput)

    // 恢复显示所有分区
    expect(screen.getByText('会话历史压缩')).toBeInTheDocument()
    expect(screen.getByText('工具运行时')).toBeInTheDocument()
    expect(screen.getByText('代理流量过滤规则')).toBeInTheDocument()
  })
})
