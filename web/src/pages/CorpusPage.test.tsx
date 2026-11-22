import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { CorpusPage } from './CorpusPage'
import { ApiError } from '@/api/client'
import {
  createCorpusEntry,
  deleteCorpusEntry,
  getCorpusEntry,
  getCorpusFacets,
  getCorpusStatus,
  importCorpus,
  listCorpus,
  previewCorpusImport,
  searchCorpus,
  updateCorpusEntry,
  type CorpusEntry,
} from '@/api/corpus'
import { listScenarios } from '@/api/config'

vi.mock('@/api/corpus', () => ({
  listCorpus: vi.fn(),
  getCorpusFacets: vi.fn(),
  getCorpusStatus: vi.fn(),
  getCorpusEntry: vi.fn(),
  createCorpusEntry: vi.fn(),
  updateCorpusEntry: vi.fn(),
  deleteCorpusEntry: vi.fn(),
  searchCorpus: vi.fn(),
  importCorpus: vi.fn(),
  previewCorpusImport: vi.fn(),
}))

vi.mock('@/api/config', () => ({ listScenarios: vi.fn() }))

const mocked = <T,>(fn: T) => fn as unknown as ReturnType<typeof vi.fn>

function entry(over: Partial<CorpusEntry> = {}): CorpusEntry {
  return {
    id: 'e1',
    title: '显眼位置的 flag 串先当诱饵再验证',
    content: '带 fake 字样的串多半是诱饵。',
    scenario: { code: 'ctf', name: 'CTF', deleted: false },
    source: 'agent',
    indexed: true,
    confirmations: 2,
    created_at: '2026-11-20T10:00:00Z',
    updated_at: '2026-11-20T11:00:00Z',
    ...over,
  }
}

let location = ''
function LocationProbe() {
  location = useLocation().search
  return null
}

function renderPage(initial = '/corpus') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initial]}>
        <CorpusPage />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  mocked(listCorpus).mockResolvedValue({ items: [entry()], total: 1 })
  mocked(getCorpusFacets).mockResolvedValue({
    scenarios: [
      { scenario: null, count: 3 },
      { scenario: { code: 'ctf', name: 'CTF', deleted: false }, count: 1 },
    ],
    sources: [{ source: 'agent', count: 4 }],
  })
  mocked(getCorpusStatus).mockResolvedValue({
    mode: 'semantic',
    embedding_model: 'emb',
    rerank_model: 'rr',
    total: 4,
    pending: 0,
  })
  mocked(getCorpusEntry).mockResolvedValue({
    entry: entry(),
    confirmations: [{ task_id: 't1', task_goal: '解 CTF 题', confirmed_at: '2026-11-20T11:00:00Z' }],
  })
  mocked(listScenarios).mockResolvedValue([
    { code: 'ctf', name: 'CTF', description: '', body: '', enabled: true, is_builtin: true, is_default: false, customized: [], shadows_builtin: false },
  ])
})

describe('CorpusPage', () => {
  it('列出知识并显示检索方式', async () => {
    renderPage()
    expect(await screen.findByText('显眼位置的 flag 串先当诱饵再验证')).toBeInTheDocument()
    expect(screen.getByText('语义检索')).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '通用（3）' })).toBeInTheDocument()
  })

  it('按「通用」筛选写入 URL，服务端按通用筛选', async () => {
    renderPage()
    await screen.findByText('显眼位置的 flag 串先当诱饵再验证')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '按适用范围筛选' }), '通用（3）')
    await waitFor(() => expect(location).toContain('general=true'))
    expect(mocked(listCorpus)).toHaveBeenLastCalledWith(
      { search: '', scenario: '', general: true, source: '' },
      1,
      50,
    )
  })

  it('点行打开详情，展示印证任务', async () => {
    renderPage()
    await userEvent.click(await screen.findByRole('row', { name: '查看知识：显眼位置的 flag 串先当诱饵再验证' }))
    const drawer = await screen.findByRole('dialog')
    expect(location).toContain('selected=e1')
    expect(await within(drawer).findByText('解 CTF 题')).toBeInTheDocument()
    expect(within(drawer).getByRole('link', { name: /任务/ })).toHaveAttribute('href', expect.stringContaining('selected=t1'))
  })

  it('编辑任务沉淀的条目：提示成为人工内容，只提交改动的字段', async () => {
    mocked(updateCorpusEntry).mockResolvedValue(entry({ source: 'expert' }))
    renderPage('/corpus?selected=e1')
    const drawer = await screen.findByRole('dialog')
    await userEvent.click(await within(drawer).findByRole('button', { name: '编辑' }))
    const content = within(drawer).getByLabelText('正文')
    await userEvent.clear(content)
    await userEvent.type(content, '新正文')
    expect(within(drawer).getByText('保存后成为人工内容，任务收尾不再改写它')).toBeInTheDocument()
    await userEvent.click(within(drawer).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(mocked(updateCorpusEntry)).toHaveBeenCalledWith('e1', { content: '新正文' }))
  })

  it('删除需确认', async () => {
    mocked(deleteCorpusEntry).mockResolvedValue(undefined)
    renderPage('/corpus?selected=e1')
    const drawer = await screen.findByRole('dialog')
    await userEvent.click(await within(drawer).findByRole('button', { name: '删除' }))
    expect(mocked(deleteCorpusEntry)).not.toHaveBeenCalled()
    const detailCalls = mocked(getCorpusEntry).mock.calls.length
    await userEvent.click(screen.getByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(mocked(deleteCorpusEntry)).toHaveBeenCalledWith('e1'))
    await waitFor(() => expect(location).not.toContain('selected='))
    await waitFor(() => expect(mocked(listCorpus).mock.calls.length).toBeGreaterThan(1))
    expect(mocked(getCorpusEntry).mock.calls.length, '已删除条目的详情不再重取').toBe(detailCalls)
  })

  it('录入的正文与已有条目重复时可打开已有条目', async () => {
    mocked(createCorpusEntry).mockRejectedValue(
      new ApiError('POST', 409, '已有正文相同的知识条目', { error: '已有正文相同的知识条目', existing_id: 'e1' }),
    )
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: '录入' }))
    const dialog = await screen.findByRole('dialog', { name: '录入知识' })
    await userEvent.type(within(dialog).getByLabelText('标题'), '标题')
    await userEvent.type(within(dialog).getByLabelText('正文'), '正文')
    await userEvent.click(within(dialog).getByRole('button', { name: '录入' }))
    expect(await within(dialog).findByText(/已有正文相同的知识条目/)).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: '查看已有条目' }))
    await waitFor(() => expect(location).toContain('selected=e1'))
  })

  it('检索测试：查询与视角写进 URL，打开条目不丢结果', async () => {
    mocked(searchCorpus).mockResolvedValue({
      mode: 'lexical',
      fallback: '重排: timeout',
      threshold: 0.05,
      hits: [{ entry: entry(), score: 0.42 }],
    })
    renderPage()
    await userEvent.click(await screen.findByRole('tab', { name: '检索测试' }))
    await userEvent.type(screen.getByLabelText('查询'), '诱饵怎么分辨')
    await userEvent.selectOptions(screen.getByLabelText('任务场景'), 'CTF')
    await userEvent.click(screen.getByRole('button', { name: '检索' }))
    expect(await screen.findByText('字面检索 · 覆盖率 ≥ 0.05 · 语义检索暂不可用：重排: timeout')).toBeInTheDocument()
    expect(screen.getByText('0.42')).toBeInTheDocument()
    expect(mocked(searchCorpus)).toHaveBeenCalledWith('诱饵怎么分辨', 'ctf')
    expect(location).toContain('view=retrieval')
    expect(location).toContain('as=ctf')

    await userEvent.click(screen.getByRole('row', { name: '查看知识：显眼位置的 flag 串先当诱饵再验证' }))
    const drawer = await screen.findByRole('dialog')
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(drawer).not.toBeInTheDocument())
    expect(screen.getByText('0.42'), '关闭详情后检索结果还在').toBeInTheDocument()
  })

  it('编辑中途关闭要先确认放弃修改', async () => {
    renderPage('/corpus?selected=e1')
    const drawer = await screen.findByRole('dialog')
    await userEvent.click(await within(drawer).findByRole('button', { name: '编辑' }))
    await userEvent.type(within(drawer).getByLabelText('标题'), '改')
    await userEvent.keyboard('{Escape}')
    const confirm = await screen.findByRole('alertdialog', { name: '放弃修改' })
    await userEvent.click(within(confirm).getByRole('button', { name: '取消' }))
    expect(within(drawer).getByLabelText('标题')).toHaveValue('显眼位置的 flag 串先当诱饵再验证改')
    await userEvent.click(within(drawer).getByRole('button', { name: '取消' }))
    await userEvent.click(await screen.findByRole('button', { name: '确认放弃' }))
    expect(await within(drawer).findByRole('button', { name: '编辑' })).toBeInTheDocument()
    expect(mocked(updateCorpusEntry)).not.toHaveBeenCalled()
  })

  it('导入先预览切分结果，跳过重复后确认导入', async () => {
    mocked(previewCorpusImport).mockResolvedValue([
      { file: 'a.md', title: '甲', content: '一', duplicate: false },
      { file: 'a.md', title: '乙', content: '二', duplicate: true },
    ])
    mocked(importCorpus).mockResolvedValue({ imported: 1, duplicates: 1 })
    renderPage()
    await userEvent.click(await screen.findByRole('button', { name: '导入' }))
    const dialog = await screen.findByRole('dialog', { name: '导入 Markdown' })
    const file = new File(['# 甲\n一\n# 乙\n二'], 'a.md', { type: 'text/markdown' })
    await userEvent.upload(within(dialog).getByLabelText('文件'), file)
    await userEvent.click(within(dialog).getByRole('button', { name: '预览' }))
    expect(await within(dialog).findByText('切出 2 条，其中 1 条与已有正文相同、导入时跳过')).toBeInTheDocument()
    expect(mocked(importCorpus)).not.toHaveBeenCalled()
    await userEvent.click(within(dialog).getByRole('button', { name: '导入 1 条' }))
    await waitFor(() => expect(mocked(importCorpus)).toHaveBeenCalledWith([file], null))
    expect(await screen.findByText('导入完成：新增 1 条，1 条与已有正文相同已跳过')).toBeInTheDocument()
  })
})

describe('CorpusPage 检索方式标识的降级态', () => {
  it('语义配置在但最近一次语义调用失败：标识显示「语义检索已降级」并带原因与时间', async () => {
    mocked(getCorpusStatus).mockResolvedValue({
      mode: 'semantic',
      degraded: '402 上游欠费',
      degraded_at: '2026-01-01T08:00:00Z',
      embedding_model: 'jina-embeddings-v5-text-small',
      rerank_model: 'jina-reranker-v2-base-multilingual',
      total: 2,
      pending: 0,
    })
    mocked(listCorpus).mockResolvedValue({ items: [], total: 0 })
    mocked(getCorpusFacets).mockResolvedValue({ scenarios: [], sources: [] })
    renderPage()
    const chip = await screen.findByText('语义检索已降级')
    expect(chip).toBeInTheDocument()
    expect(chip.getAttribute('title')).toContain('402 上游欠费')
    expect(chip.getAttribute('title')).toContain('修复后自动恢复语义')
  })
})
