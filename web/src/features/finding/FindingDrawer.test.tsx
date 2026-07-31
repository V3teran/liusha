import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FindingDrawer } from './FindingDrawer'
import type { FindingRow } from '@/api/types'

function makeFinding(overrides: Partial<FindingRow> = {}): FindingRow {
  return {
    id: 'f-1',
    severity: 'high',
    summary: 'SQL 注入漏洞',
    host: 'example.com',
    mode: 'active',
    status: 'open',
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

describe('FindingDrawer', () => {
  beforeEach(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    })
  })

  it('open=false 时不渲染 finding 内容', () => {
    const finding = makeFinding()
    render(<FindingDrawer open={false} finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.queryByText('SQL 注入漏洞')).toBeNull()
  })

  it('open+finding 时渲染 summary/severity/host', () => {
    const finding = makeFinding({ severity: 'critical', host: 'evil.example.com', target: { method: 'GET', path: '/a' } })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByText('SQL 注入漏洞')).toBeTruthy()
    expect(screen.getByText('严重')).toBeTruthy() // critical → 严重
    expect(screen.getByText(/evil.example.com/)).toBeTruthy()
    expect(screen.getByText('GET')).toBeTruthy()
  })

  it('定位行（方法+host+路径）带复制按钮，点击复制拼接后的完整目标', async () => {
    const writeText = navigator.clipboard.writeText
    const finding = makeFinding({ host: 'evil.example.com', target: { method: 'POST', path: '/login' } })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)

    // 定位行的复制按钮是 compact（仅图标，title="复制"），与证据区的文字按钮区分。
    const copyBtn = screen.getByTitle('复制')
    fireEvent.click(copyBtn)

    expect(writeText).toHaveBeenCalledWith('POST evil.example.com /login')
    expect(await screen.findByTitle('已复制')).toBeTruthy()
  })

  it('finding 为 null 时不渲染内容区块', () => {
    render(<FindingDrawer open finding={null} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.queryByText('处置')).toBeNull()
  })

  it('编辑态从 finding 初始化：status/severity/note', () => {
    const finding = makeFinding({ status: 'confirmed', severity: 'medium', triage_note: '已核实' })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByDisplayValue('已确认')).toBeTruthy()
    expect(screen.getByDisplayValue('medium')).toBeTruthy()
    expect(screen.getByPlaceholderText(/处置备注/)).toHaveValue('已核实')
  })

  it('未修改字段时保存按钮 disabled；修改后启用且点击触发 onSave', async () => {
    const user = userEvent.setup()
    const finding = makeFinding({ status: 'open', severity: 'high', triage_note: '' })
    const onSave = vi.fn()
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={onSave} />)

    const saveBtn = screen.getByRole('button', { name: '保存' })
    expect(saveBtn).toBeDisabled()

    const noteBox = screen.getByPlaceholderText(/处置备注/)
    await user.type(noteBox, '误报，已复核')

    expect(saveBtn).not.toBeDisabled()
    await user.click(saveBtn)

    expect(onSave).toHaveBeenCalledWith({
      id: 'f-1',
      status: 'open',
      severity: 'high',
      note: '误报，已复核',
    })
  })

  it('切换状态下拉后 dirty 变为 true', async () => {
    const user = userEvent.setup()
    const finding = makeFinding({ status: 'open' })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)

    const saveBtn = screen.getByRole('button', { name: '保存' })
    expect(saveBtn).toBeDisabled()

    const statusSelect = screen.getByDisplayValue('待处理')
    await user.selectOptions(statusSelect, 'confirmed')

    expect(saveBtn).not.toBeDisabled()
  })

  it('evidence: cmd/curl/repro 类 key 渲染为代码块并带复制按钮', () => {
    const finding = makeFinding({
      evidence: {
        repro_cmd: 'curl -X POST http://x',
        observation: '响应异常',
      },
    })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)

    // repro_cmd → 代码块 + 复制按钮
    expect(screen.getByText('复现命令')).toBeTruthy()
    const copyButtons = screen.getAllByText('复制')
    expect(copyButtons.length).toBe(1) // 只有 code kind 才有复制按钮

    // observation → 纯文本，无复制按钮
    expect(screen.getByText('观察')).toBeTruthy()
    expect(screen.getByText('响应异常')).toBeTruthy()
  })

  it('evidence: 对象/数组值渲染为 JSON 折行，同样带复制按钮', () => {
    const finding = makeFinding({
      evidence: {
        vulnerable_endpoints: ['/a', '/b'],
      },
    })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    // Radix Dialog 内容 portal 到 document.body，不在 render() 的 container 内。
    const pre = document.body.querySelector('pre')
    expect(pre?.textContent).toBe(JSON.stringify(['/a', '/b'], null, 2))
    // json 块和 code 块一样是代码展示，同样需要复制入口（之前实现漏掉了 json 分支）。
    expect(screen.getByText('复制')).toBeTruthy()
  })

  it('evidence 为空/未提供时不渲染证据区块', () => {
    const finding = makeFinding({ evidence: undefined })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.queryByText(/证据/)).toBeNull()
  })

  it('evidence 中 null/空字符串值被跳过', () => {
    const finding = makeFinding({
      evidence: { payload: '', analysis: null as unknown as string },
    })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.queryByText(/证据/)).toBeNull()
  })

  it('点击复制按钮调用 clipboard.writeText 并短暂显示已复制', async () => {
    // 注意：userEvent.setup() 会用自己的 clipboard 覆盖 navigator.clipboard，
    // 这里用 fireEvent 保留 beforeEach 里注入的 mock。
    const writeText = navigator.clipboard.writeText
    const finding = makeFinding({ evidence: { payload: "' OR 1=1" } })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)

    const copyBtn = screen.getByText('复制')
    fireEvent.click(copyBtn)

    expect(writeText).toHaveBeenCalledWith("' OR 1=1")
    expect(await screen.findByText('已复制')).toBeTruthy()
  })

  it('remediation 存在时渲染修复建议区块', () => {
    const finding = makeFinding({ remediation: '使用参数化查询' })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByText('修复建议')).toBeTruthy()
    expect(screen.getByText('使用参数化查询')).toBeTruthy()
  })

  it('cwe_id/owasp_category 存在时渲染在元信息区', () => {
    const finding = makeFinding({ cwe_id: 'CWE-89', owasp_category: 'A03:2021' })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByText('CWE-89')).toBeTruthy()
    expect(screen.getByText('A03:2021')).toBeTruthy()
  })

  it('切换到不同 finding 时重新初始化编辑态', () => {
    const finding1 = makeFinding({ id: 'f-1', status: 'open', triage_note: 'note1' })
    const { rerender } = render(<FindingDrawer open finding={finding1} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByPlaceholderText(/处置备注/)).toHaveValue('note1')

    const finding2 = makeFinding({ id: 'f-2', status: 'confirmed', triage_note: 'note2' })
    rerender(<FindingDrawer open finding={finding2} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByPlaceholderText(/处置备注/)).toHaveValue('note2')
  })
})
