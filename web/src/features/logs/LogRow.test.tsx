import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { LogRow } from './LogRow'
import type { LogEntry } from '@/api/types'

const entry = (over: Partial<LogEntry> = {}): LogEntry =>
  ({
    service: 'api',
    instance: '4242@host.docker.internal',
    offset: 1,
    time: '2026-11-25T01:02:03.456Z',
    level: 'info',
    message: 'request',
    caller: 'api/server.go:88',
    fields: { method: 'GET', path: '/api/scenarios', status: 200, latency_ms: 2, client: '::1' },
    ...over,
  }) as LogEntry

// 一行日志的可访问名从内容生成，就是屏幕上的文字本身（WCAG 2.5.3「标签包含于名称」：语音控制
// 用户照着屏幕念出来的字，要能选中这一行）。这条规则要求可见文字原样连续地出现在名字里——
// 此前行上写死了 aria-label「日志：<摘要>」，屏幕上没有「日志」两个字，对不上。
describe('LogRow 可访问名', () => {
  it('就是屏幕上的可见文字，按显示顺序，各列之间有读屏可辨的间隔', () => {
    render(<LogRow e={entry()} open={false} onToggle={vi.fn()} />)
    const row = screen.getByRole('button')
    expect(row.hasAttribute('aria-label')).toBe(false)
    const name = row.textContent ?? ''
    // 可见文字都在名字里，顺序与显示一致：时间、级别、服务、摘要、调用位置
    const order = ['info', 'api', 'GET /api/scenarios 200', 'api/server.go:88'].map((s) => name.indexOf(s))
    expect(order.every((i) => i >= 0)).toBe(true)
    expect([...order].sort((a, b) => a - b)).toEqual(order)
    // 屏幕上没有的词不在名字里
    expect(name).not.toContain('日志')
    // 相邻两列之间不是直接粘在一起
    expect(name).toMatch(/info\s+\[/)
    expect(name).toMatch(/\]\s+GET/)
  })

  it('完整的时间与实例只在悬停提示里，读屏从名字里读到它们', () => {
    render(<LogRow e={entry()} open={false} onToggle={vi.fn()} />)
    const name = screen.getByRole('button').textContent ?? ''
    expect(name).toContain('4242@host.docker.internal')
  })

  it('回车与空格展开，点击展开', async () => {
    const user = userEvent.setup()
    const onToggle = vi.fn()
    render(<LogRow e={entry()} open={false} onToggle={onToggle} />)
    await user.click(screen.getByRole('button'))
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(onToggle).toHaveBeenCalledTimes(3)
  })

  it('展开后显示完整字段', () => {
    render(<LogRow e={entry()} open onToggle={vi.fn()} />)
    expect(screen.getByText(/instance: 4242@host\.docker\.internal/)).toBeTruthy()
    expect(screen.getByText(/caller: api\/server\.go:88/)).toBeTruthy()
  })
})
