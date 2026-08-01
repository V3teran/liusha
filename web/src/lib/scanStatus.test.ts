import { describe, expect, it } from 'vitest'
import { scanStatusMeta } from './scanStatus'

describe('scanStatusMeta', () => {
  it('active → 进行中', () => {
    const m = scanStatusMeta('active')
    expect(m.label).toBe('进行中')
    expect(m.key).toBe('active')
  })

  it('completed → 已完成', () => {
    const m = scanStatusMeta('completed')
    expect(m.label).toBe('已完成')
    expect(m.key).toBe('done')
  })

  it('aborted → 已中止', () => {
    const m = scanStatusMeta('aborted')
    expect(m.label).toBe('已中止')
    expect(m.key).toBe('aborted')
  })

  it('undefined/空/未知 → 会话（idle）', () => {
    expect(scanStatusMeta(undefined).key).toBe('idle')
    expect(scanStatusMeta('').key).toBe('idle')
    expect(scanStatusMeta('weird').key).toBe('idle')
  })
})
