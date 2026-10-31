import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TaskStatusCard } from './TaskStatusCard'
import type { TimelineEventMeta } from '@/api/types'

const md = (over: Partial<TimelineEventMeta>): TimelineEventMeta => ({
  kind: 'task_status',
  status: 'completed',
  steps: 0,
  actions: 0,
  findings: 0,
  stop_reason: '',
  reason: '',
  tool_name: '',
  args: '',
  result: '',
  duration_ms: 0,
  err: '',
  text: '',
  thinking: '',
  agent_name: '',
  in_tokens: 0,
  out_tokens: 0,
  latency_ms: 0,
  ...over,
})

describe('TaskStatusCard', () => {
  it('completed：标签 + 报告字段（推理步数/发现数；actions 与 stop_reason 留 wire 不渲染）', () => {
    render(<TaskStatusCard md={md({ status: 'completed', steps: 22, actions: 8, findings: 1, stop_reason: 'task_converged' })} />)
    expect(screen.getByText('扫描完成')).toBeTruthy()
    expect(screen.getByText(/22 步/)).toBeTruthy()
    expect(screen.getByText(/1 个发现/)).toBeTruthy()
    // 动作数与收敛枚举不直出人面（自然收敛无需复述，动作细节在叙述卡里）
    expect(screen.queryByText(/个动作/)).toBeNull()
    expect(screen.queryByText(/任务收敛|task_converged/)).toBeNull()
  })

  it('dispatched：仅标签，无报告字段行', () => {
    render(<TaskStatusCard md={md({ status: 'dispatched' })} />)
    expect(screen.getByText('已下发扫描')).toBeTruthy()
    expect(screen.queryByText(/步/)).toBeNull()
  })

  it('aborted：携带原因', () => {
    render(<TaskStatusCard md={md({ status: 'aborted', reason: '用户停止' })} />)
    expect(screen.getByText('扫描已停止')).toBeTruthy()
    expect(screen.getByText(/用户停止/)).toBeTruthy()
  })

  it('failed：携带错误原因', () => {
    render(<TaskStatusCard md={md({ status: 'failed', reason: 'sandbox unavailable' })} />)
    expect(screen.getByText('扫描执行失败')).toBeTruthy()
    expect(screen.getByText(/sandbox unavailable/)).toBeTruthy()
  })

  it('findings=0 时不显示「0 个发现」（无坐实主张不展示空字段）', () => {
    render(<TaskStatusCard md={md({ status: 'completed', steps: 3, findings: 0 })} />)
    expect(screen.queryByText(/0 个发现/)).toBeNull()
  })
})
