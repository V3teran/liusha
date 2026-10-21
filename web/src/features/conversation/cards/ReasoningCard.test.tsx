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
    render(<ReasoningCard text="x" agentName="evaluator" />)
    expect(screen.getByText('裁决')).toBeTruthy()
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

  it('正文为 JSON 形态时折叠为「结构化输出」（残留协议兜底，只格式化不解释）', () => {
    const { container } = render(<ReasoningCard text='{"should_continue":false,"actions":[]}' />)
    expect(screen.getByText('结构化输出')).toBeTruthy()
    // 不再以 markdown 正文直出 JSON
    expect(container.querySelector('.markdown-body')).toBeNull()
  })

  it('raw 存在时渲染「原始输出」折叠（正文为解析后叙述）', () => {
    render(<ReasoningCard text="任务收敛，不再派发动作。" raw='{"should_continue":false}' />)
    expect(screen.getByText('原始输出')).toBeTruthy()
    expect(screen.getByText('任务收敛，不再派发动作。')).toBeTruthy()
  })

  it('流式态不渲染折叠区（JSON 判定与 raw 都等终稿）', () => {
    render(<ReasoningCard text='{"a":1}' raw='{"a":1}' streaming />)
    expect(screen.queryByText('结构化输出')).toBeNull()
    expect(screen.queryByText('原始输出')).toBeNull()
  })

  it('终稿正文为空时渲染占位（模型思考耗尽输出预算的轮次），流式空正文不带占位', () => {
    const { rerender } = render(<ReasoningCard text="" thinking="很长的思考" />)
    expect(screen.getByText('（本轮未产出正文，仅有内部思考）')).toBeTruthy()
    rerender(<ReasoningCard text="  " />)
    expect(screen.getByText('（本轮未产出正文，仅有内部思考）')).toBeTruthy()
    rerender(<ReasoningCard text="" streaming />)
    expect(screen.queryByText('（本轮未产出正文，仅有内部思考）')).toBeNull()
  })
})
