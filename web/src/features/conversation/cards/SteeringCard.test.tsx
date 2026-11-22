import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SteeringCard } from './SteeringCard'
import type { Message, TimelineEventMeta } from '@/api/types'

const msg = (content: string, md: Partial<TimelineEventMeta>): Message =>
  ({
    seq: 1,
    conversation_id: 'c1',
    role: 'user',
    kind: 'event',
    content,
    metadata: { kind: 'steering', ...md } as TimelineEventMeta,
    created_at: '2026-01-01T00:00:00Z',
  }) as Message

describe('SteeringCard', () => {
  it('调整目标：展示新目标全文与撤回数', () => {
    render(<SteeringCard msg={msg('改测注册页\n账号 admin', { op: 'goal_adjusted', withdrawn: 2 })} />)
    expect(screen.getByText('目标已调整')).toBeTruthy()
    expect(screen.getByText('撤回 2 个未开始的规划动作')).toBeTruthy()
    expect(screen.getByText(/改测注册页/)).toBeTruthy()
  })

  it('指派动作：展示指令与优先级', () => {
    render(<SteeringCard msg={msg('复测登录接口', { op: 'action_assigned', priority: 'critical' })} />)
    expect(screen.getByText('已指派动作')).toBeTruthy()
    expect(screen.getByText('P0 关键')).toBeTruthy()
  })

  it('否决动作：正文是原因', () => {
    render(<SteeringCard msg={msg('超出授权范围', { op: 'action_rejected' })} />)
    expect(screen.getByText('原因：超出授权范围')).toBeTruthy()
  })
})
