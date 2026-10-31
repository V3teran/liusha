import { describe, expect, it } from 'vitest'
import { taskStatusMeta } from './taskStatus'

describe('taskStatusMeta', () => {
  it('active → 进行中', () => {
    const m = taskStatusMeta('active')
    expect(m.label).toBe('进行中')
    expect(m.key).toBe('active')
  })

  it('completed → 已完成', () => {
    const m = taskStatusMeta('completed')
    expect(m.label).toBe('已完成')
    expect(m.key).toBe('completed')
  })

  it('aborted → 已中止', () => {
    const m = taskStatusMeta('aborted')
    expect(m.label).toBe('已中止')
    expect(m.key).toBe('aborted')
  })

  it('undefined/空/未知 → 会话（idle）', () => {
    expect(taskStatusMeta(undefined).key).toBe('idle')
    expect(taskStatusMeta('').key).toBe('idle')
    expect(taskStatusMeta('weird').key).toBe('idle')
  })
})
