import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TaskStatusCard } from './TaskStatusCard'
import type { ScanEvent } from '@/api/types'

const md = (over: Partial<ScanEvent>): ScanEvent => ({
  kind: 'task_status',
  status: 'completed',
  steps: 0,
  findings: 0,
  stop_why: '',
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
  it('completed：标签 + 报告字段（步数/漏洞/收敛原因）', () => {
    render(<TaskStatusCard md={md({ status: 'completed', steps: 15, findings: 2, stop_why: 'task_converged' })} />)
    expect(screen.getByText('扫描完成')).toBeTruthy()
    expect(screen.getByText(/15 步/)).toBeTruthy()
    expect(screen.getByText(/2 个漏洞/)).toBeTruthy()
    expect(screen.getByText(/task_converged/)).toBeTruthy()
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

  it('findings=0 时不显示「0 个漏洞」（信息类任务无漏洞不展示空字段）', () => {
    render(<TaskStatusCard md={md({ status: 'completed', steps: 3, findings: 0 })} />)
    expect(screen.queryByText(/0 个漏洞/)).toBeNull()
  })
})
