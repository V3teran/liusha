import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { FindingCard } from './FindingCard'

describe('FindingCard', () => {
  it('渲染 summary/severity 徽章大写', () => {
    render(<FindingCard args={JSON.stringify({ summary: 'SQL 注入', severity: 'high' })} />)
    expect(screen.getByText('SQL 注入')).toBeTruthy()
    expect(screen.getByText('HIGH')).toBeTruthy()
  })

  it('缺 summary 时回退"(无标题)"', () => {
    render(<FindingCard args={JSON.stringify({ severity: 'low' })} />)
    expect(screen.getByText('(无标题)')).toBeTruthy()
  })

  it('缺 severity 时回退 info', () => {
    render(<FindingCard args={JSON.stringify({ summary: 'x' })} />)
    expect(screen.getByText('INFO')).toBeTruthy()
  })

  it('有 target.path 无 method 时回退 GET', () => {
    render(<FindingCard args={JSON.stringify({ summary: 'x', target: { path: '/login' } })} />)
    expect(screen.getByText('GET')).toBeTruthy()
    expect(screen.getByText('/login')).toBeTruthy()
  })

  it('有 method 时显示实际 method', () => {
    render(<FindingCard args={JSON.stringify({ summary: 'x', target: { method: 'POST', path: '/api' } })} />)
    expect(screen.getByText('POST')).toBeTruthy()
  })

  it('无 target 时不渲染位置信息', () => {
    const { container } = render(<FindingCard args={JSON.stringify({ summary: 'x' })} />)
    expect(container.querySelector('.font-mono.text-muted')).toBeNull()
  })

  it('有 cwe_id 时渲染 CWE 标签', () => {
    render(<FindingCard args={JSON.stringify({ summary: 'x', cwe_id: 'CWE-89' })} />)
    expect(screen.getByText('CWE-89')).toBeTruthy()
  })

  it('有 owasp_category 时渲染 OWASP 标签', () => {
    render(<FindingCard args={JSON.stringify({ summary: 'x', owasp_category: 'A03:2021' })} />)
    expect(screen.getByText('A03:2021')).toBeTruthy()
  })

  it('无 cwe/owasp 时不渲染对应标签', () => {
    render(<FindingCard args={JSON.stringify({ summary: 'x' })} />)
    expect(screen.queryByText(/CWE/)).toBeNull()
  })

  it('非法 JSON 参数不崩，回退空对象', () => {
    const { container } = render(<FindingCard args="not json" />)
    expect(container.querySelector('[data-card="finding"]')).toBeTruthy()
    expect(screen.getByText('(无标题)')).toBeTruthy()
  })

  it('空字符串参数不崩', () => {
    const { container } = render(<FindingCard args="" />)
    expect(container.querySelector('[data-card="finding"]')).toBeTruthy()
  })
})
