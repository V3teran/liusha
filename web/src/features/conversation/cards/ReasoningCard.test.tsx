import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ReasoningCard, RoundHeader } from './ReasoningCard'

// 身份行：一轮是谁的、第几步、用了多少。时间线把它排在工具调用与结论之前。
describe('RoundHeader', () => {
  it('非流式态显示「推理」', () => {
    render(<RoundHeader />)
    expect(screen.getByText('推理')).toBeTruthy()
    expect(screen.queryByText('推理中')).toBeNull()
  })

  it('流式态显示「推理中」', () => {
    render(<RoundHeader streaming />)
    expect(screen.getByText('推理中')).toBeTruthy()
    expect(screen.queryByText('推理')).toBeNull()
  })

  it('有 agentName 时显示中文标签', () => {
    render(<RoundHeader agentName="evaluator" />)
    expect(screen.getByText('评估')).toBeTruthy()
  })

  it('step 存在时显示「第 N 步」；为 0 或未传时不显示', () => {
    const { rerender } = render(<RoundHeader step={3} />)
    expect(screen.getByText('第 3 步')).toBeTruthy()
    rerender(<RoundHeader step={0} />)
    expect(screen.queryByText(/第 \d+ 步/)).toBeNull()
  })

  it('token / 耗时存在时渲染 chip（千位数用 k 简写）', () => {
    const { container } = render(<RoundHeader inTokens={1500} outTokens={800} latencyMs={2500} />)
    const text = container.textContent ?? ''
    expect(text).toContain('1.5k')
    expect(text).toContain('800')
    expect(text).toContain('2.5s')
  })

  it('耗时小于 1000ms 时用 ms 单位', () => {
    render(<RoundHeader inTokens={5} latencyMs={300} />)
    expect(screen.getByText(/300ms/)).toBeTruthy()
  })

  it('没有用量元信息时不渲染 chip', () => {
    const { container } = render(<RoundHeader />)
    expect(container.querySelectorAll('[title="输入 token"], [title="耗时"]')).toHaveLength(0)
  })
})

// 结论：内部思考（折叠）与正文。它不带身份行。
describe('ReasoningCard', () => {
  it('渲染 markdown 正文', () => {
    const { container } = render(<ReasoningCard text="**重点**内容" />)
    expect(container.querySelector('strong')?.textContent).toBe('重点')
  })

  it('终稿带内部思考时折叠为「内部思考」', () => {
    render(<ReasoningCard text="结论" thinking="先想一下" />)
    expect(screen.getByText('内部思考')).toBeTruthy()
  })

  it('正文为 JSON 形态时折叠为「结构化输出」（残留协议兜底，只格式化不解释）', () => {
    const { container } = render(<ReasoningCard text='{"should_continue":false,"actions":[]}' />)
    expect(screen.getByText('结构化输出')).toBeTruthy()
    // 不再以 markdown 正文直出 JSON
    expect(container.querySelector('.markdown-body')).toBeNull()
  })

  it('叙述正文直出，不再有「原始输出」折叠（面分离：原文在模型调用页按调用可查）', () => {
    render(<ReasoningCard text="任务收敛，不再派发动作。" />)
    expect(screen.getByText('任务收敛，不再派发动作。')).toBeTruthy()
    expect(screen.queryByText('原始输出')).toBeNull()
  })

  it('流式态：闪烁光标；折叠区都等终稿', () => {
    const { container } = render(<ReasoningCard text='{"a":1}' thinking="x" agentName="executor" streaming />)
    expect(container.querySelector('.animate-pulse')).toBeTruthy()
    expect(screen.queryByText('结构化输出')).toBeNull()
    expect(screen.queryByText('内部思考')).toBeNull()
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
