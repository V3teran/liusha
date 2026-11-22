import { describe, expect, it, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { FindingDrawer } from './FindingDrawer'
import type { FindingRow } from '@/api/types'

function makeFinding(overrides: Partial<FindingRow> = {}): FindingRow {
  return {
    id: 'f-1',
    seq: 1,
    severity: 'high',
    summary: 'SQL 注入漏洞',
    host: 'example.com',
    class: 'other',
    class_name: '其他',
    cwe: '',
    source: 'direct',
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
    // 「严重」出现在头部徽章与严重度下拉选项两处，断言存在即可。
    expect(screen.getAllByText('严重').length).toBeGreaterThan(0) // critical → 严重
    expect(screen.getByText(/evil.example.com/)).toBeTruthy()
    // 位置行是「方法 host 路径」同一文本节点，联合断言。
    expect(screen.getByText(/GET evil\.example\.com\/a/)).toBeTruthy()
  })

  it('跳转区排在基本信息之前：回到产出上下文是最先用到的出口', () => {
    const finding = makeFinding({ task_id: 't-1', conversation_id: 'c-1' })
    render(
      <MemoryRouter>
        <FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />
      </MemoryRouter>,
    )
    const jump = screen.getByRole('heading', { name: '跳转' })
    const basic = screen.getByRole('heading', { name: '基本信息' })
    expect(jump.compareDocumentPosition(basic) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // 只看这个发现所属的：任务、对话、探索图（发现属于任务，不跳回发现自身）
    expect(screen.getByRole('link', { name: '任务' }).getAttribute('href')).toBe('/tasks?task=t-1&selected=t-1')
    expect(screen.getByRole('link', { name: '对话' }).getAttribute('href')).toBe('/conversations/direct?conversation=c-1')
    expect(screen.getByRole('link', { name: '探索图' }).getAttribute('href')).toBe('/exploration-graph?task=t-1')
    expect(screen.queryByRole('link', { name: '发现' })).toBeNull()
  })

  it('探索图定位到坐实这个发现的结果节点', () => {
    const finding = makeFinding({ task_id: 't-1', result_node_id: 'r-9' })
    render(
      <MemoryRouter>
        <FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />
      </MemoryRouter>,
    )
    expect(screen.getByRole('link', { name: '探索图' }).getAttribute('href')).toBe('/exploration-graph?task=t-1&selected=r-9')
  })

  it('finding 为 null 时不渲染内容区块', () => {
    render(<FindingDrawer open finding={null} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.queryByText('处置')).toBeNull()
  })

  it('编辑态从 finding 初始化：status/severity/note', () => {
    const finding = makeFinding({ status: 'confirmed', severity: 'medium', triage_note: '已核实' })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    // select 的 display value 是选中 option 的文案（confirmed → 已确认、medium → 中危）。
    expect(screen.getByDisplayValue('已确认')).toBeTruthy()
    expect(screen.getByDisplayValue('中危')).toBeTruthy()
    expect(screen.getByPlaceholderText(/处置思路/)).toHaveValue('已核实')
  })

  it('未修改字段时保存按钮 disabled；修改后启用且点击触发 onSave', async () => {
    const user = userEvent.setup()
    const finding = makeFinding({ status: 'open', severity: 'high', triage_note: '' })
    const onSave = vi.fn()
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={onSave} />)

    const saveBtn = screen.getByRole('button', { name: '保存处置' })
    expect(saveBtn).toBeDisabled()

    const noteBox = screen.getByPlaceholderText(/处置思路/)
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

    const saveBtn = screen.getByRole('button', { name: '保存处置' })
    expect(saveBtn).toBeDisabled()

    const statusSelect = screen.getByDisplayValue('待处理')
    await user.selectOptions(statusSelect, 'confirmed')

    expect(saveBtn).not.toBeDisabled()
  })

  // 证据链区（真库形态样本：active:full 实测 finding 的结构）
  const chainEvaluation = {
    verification_tier: 'full_gate',
    judge_reasoning: '验证请求带 cmd=id 返回 uid=33，基线无此特征，主张成立',
    evidence: {
      url: 'http://t:8080/shell.php?cmd=id',
      method: 'GET',
      assert_reasons: ['status_code=200 ✓', 'body 含 "uid=33(www-data)" ✓'],
      status_code: 200,
      baseline_status_code: 200,
      response_body_snippet: 'uid=33(www-data)',
    },
  }
  const chainRepro = {
    kind: 'http',
    recipe: {
      request: { method: 'GET', url: 'http://t:8080/shell.php?cmd=id', headers: { Cookie: 's=a' } },
      baseline: { url: 'http://t:8080/shell.php', method: 'GET' },
    },
  }

  it('证据链：复现命令（curl）/ 断言明细 / 验证响应 / 评估理由齐备渲染', () => {
    const finding = makeFinding({ evaluation: chainEvaluation, repro: chainRepro })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)

    expect(screen.getByText('证据链')).toBeTruthy()
    expect(screen.getByText('复现命令')).toBeTruthy()
    expect(screen.getByText('机器证据')).toBeTruthy()
    expect(screen.getByText('评估理由')).toBeTruthy()
    // curl 命令带配方 URL 与 header
    expect(document.body.textContent).toContain("curl -X GET 'http://t:8080/shell.php?cmd=id'")
    expect(document.body.textContent).toContain("-H 'Cookie: s=a'")
    // 断言逐条
    expect(screen.getByText(/status_code=200/)).toBeTruthy()
    // 验证响应片段（响应 pre 与断言行都含 uid——断言至少出现）
    expect(screen.getAllByText(/uid=33/).length).toBeGreaterThanOrEqual(1)
    // 基线对照说明
    expect(screen.getByText(/对照基线/)).toBeTruthy()
    // 档位徽章
    expect(screen.getByText('机器复放 + 终判')).toBeTruthy()
  })

  it('证据链：repro/evaluation 缺席时整区不渲染', () => {
    const finding = makeFinding({ evaluation: undefined, repro: undefined })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.queryByText('证据链')).toBeNull()
  })

  it('复现命令可复制：点击调 clipboard 并短暂显示已复制', async () => {
    const writeText = navigator.clipboard.writeText
    const finding = makeFinding({ evaluation: chainEvaluation, repro: chainRepro })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)

    const copyBtn = screen.getAllByRole('button', { name: '复制' })[0]
    fireEvent.click(copyBtn)
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('curl -X GET'))
    expect(await screen.findByRole('button', { name: '已复制' })).toBeTruthy()
  })

  it('remediation 存在时渲染修复建议区块', () => {
    const finding = makeFinding({ remediation: '使用参数化查询' })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByText('修复建议')).toBeTruthy()
    expect(screen.getByText('使用参数化查询')).toBeTruthy()
  })

  it('元信息区显示发现类型与 CWE，CWE 链到 MITRE 的定义页；没有 CWE 的不显示这一行', () => {
    const { rerender } = render(
      <FindingDrawer
        open
        finding={makeFinding({ class: 'sql_injection', class_name: 'SQL 注入', cwe: 'CWE-89' })}
        onOpenChange={vi.fn()}
        onSave={vi.fn()}
      />,
    )
    expect(screen.getByText('SQL 注入')).toBeTruthy()
    const link = screen.getByRole('link', { name: /CWE-89/ })
    expect(link).toHaveAttribute('href', 'https://cwe.mitre.org/data/definitions/89.html')
    expect(link).toHaveAttribute('target', '_blank')
    rerender(<FindingDrawer open finding={makeFinding()} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByText('其他')).toBeTruthy()
    expect(screen.queryByText('CWE')).toBeNull()
  })

  it('切换到不同 finding 时重新初始化编辑态', () => {
    const finding1 = makeFinding({ id: 'f-1', status: 'open', triage_note: 'note1' })
    const { rerender } = render(<FindingDrawer open finding={finding1} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByPlaceholderText(/处置思路/)).toHaveValue('note1')

    const finding2 = makeFinding({ id: 'f-2', status: 'confirmed', triage_note: 'note2' })
    rerender(<FindingDrawer open finding={finding2} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByPlaceholderText(/处置思路/)).toHaveValue('note2')
  })
})

describe('FindingDrawer 可访问性', () => {
  // axe 实测：关闭钮无可访问名称、处置表单的 label 未关联控件、可横向滚动的 pre 键盘不可达。
  it('关闭钮有名称，处置控件可按标签定位', () => {
    const finding = makeFinding({ status: 'confirmed', severity: 'medium' })
    render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    expect(screen.getByRole('button', { name: '关闭' })).toBeTruthy()
    expect(screen.getByLabelText('状态')).toHaveValue('confirmed')
    expect(screen.getByLabelText('严重度')).toHaveValue('medium')
    expect(screen.getByLabelText('备注').tagName).toBe('TEXTAREA')
  })

  it('复现命令块可被键盘聚焦（可滚动区域）', () => {
    const finding = makeFinding({
      repro: { kind: 'http', recipe: { request: { method: 'GET', url: 'http://t/a?q=1' } }, assert: {} },
    } as Partial<FindingRow>)
    const { container } = render(<FindingDrawer open finding={finding} onOpenChange={vi.fn()} onSave={vi.fn()} />)
    const pres = [...(container.ownerDocument.querySelectorAll('pre') as NodeListOf<HTMLPreElement>)]
    expect(pres.length).toBeGreaterThan(0)
    for (const pre of pres) expect(pre.tabIndex).toBe(0)
  })
})
