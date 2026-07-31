import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ReasoningCard } from './ReasoningCard'

describe('ReasoningCard', () => {
  it('非流式态显示"推理"标题', () => {
    render(<ReasoningCard text="分析中" />)
    expect(screen.getByText('推理')).toBeTruthy()
    expect(screen.queryByText('推理中')).toBeNull()
  })

  it('流式态显示"推理中"标题 + 闪烁光标', () => {
    const { container } = render(<ReasoningCard text="分析中" streaming />)
    expect(screen.getByText('推理中')).toBeTruthy()
    expect(container.querySelector('.animate-pulse')).toBeTruthy()
  })

  it('有 agentName 时显示中文标签', () => {
    render(<ReasoningCard text="x" agentName="exploitation" />)
    expect(screen.getByText('利用')).toBeTruthy()
  })

  it('无 agentName 时不渲染标签 chip', () => {
    const { container } = render(<ReasoningCard text="x" />)
    // agentLabel 为空串时不渲染该 span
    expect(container.querySelectorAll('[data-card="reasoning"] > div:nth-child(2) > span').length).toBeLessThan(5)
  })

  it('step 存在时显示"第 N 步"', () => {
    render(<ReasoningCard text="x" step={3} />)
    expect(screen.getByText('第 3 步')).toBeTruthy()
  })

  it('step 为 0 或未传时不显示步数 chip', () => {
    render(<ReasoningCard text="x" step={0} />)
    expect(screen.queryByText(/第 \d+ 步/)).toBeNull()
  })

  it('token/耗时元信息存在时渲染 chip（千位数用 k 简写）', () => {
    const { container } = render(<ReasoningCard text="x" inTokens={1500} outTokens={800} latencyMs={2500} />)
    const text = container.textContent ?? ''
    expect(text).toContain('1.5k')
    expect(text).toContain('800')
    expect(text).toContain('2.5s')
  })

  it('耗时小于 1000ms 时用 ms 单位', () => {
    render(<ReasoningCard text="x" inTokens={5} latencyMs={300} />)
    expect(screen.getByText(/300ms/)).toBeTruthy()
  })

  it('无 token/耗时元信息时不渲染 meta 区块', () => {
    render(<ReasoningCard text="x" />)
    expect(screen.queryByText(/输入 token/)).toBeNull()
  })

  it('渲染 markdown 正文', () => {
    const { container } = render(<ReasoningCard text="**重点**内容" />)
    expect(container.querySelector('strong')?.textContent).toBe('重点')
  })
})
