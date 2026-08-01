import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SpawnCard } from './SpawnCard'

describe('SpawnCard', () => {
  it('派发开始态：显示目标 agent 与 brief', () => {
    render(<SpawnCard args={JSON.stringify({ subagent_type: 'reconnaissance', description: '扫描目标站点' })} />)
    expect(screen.getByText('派发')).toBeTruthy()
    expect(screen.getByText('侦察')).toBeTruthy()
    expect(screen.getByText('扫描目标站点')).toBeTruthy()
  })

  it('派发完成态（成功）：显示耗时', () => {
    render(<SpawnCard done durationMs={5000} err="" />)
    expect(screen.getByText('派发完成')).toBeTruthy()
    expect(screen.getByText('✓')).toBeTruthy()
    expect(screen.getByText(/5\.0s/)).toBeTruthy()
  })

  it('派发完成态（失败）：显示错误信息', () => {
    render(<SpawnCard done err="连接超时" />)
    expect(screen.getByText('派发失败')).toBeTruthy()
    expect(screen.getByText('✗')).toBeTruthy()
    expect(screen.getByText('连接超时')).toBeTruthy()
  })

  it('args 非法 JSON 不崩，回退默认标签', () => {
    render(<SpawnCard args="not json" />)
    expect(screen.getByText('子代理')).toBeTruthy()
  })

  it('无 args 时不崩', () => {
    const { container } = render(<SpawnCard />)
    expect(container.querySelector('[data-card="spawn"]')).toBeTruthy()
  })
})
