import { describe, expect, it } from 'vitest'
import { isoToLocalInput, localInputToIso, syncIsoWithLocalInput } from './datetime'

describe('localInputToIso', () => {
  it('空串返回空串（未设置时间边界）', () => {
    expect(localInputToIso('')).toBe('')
  })

  it('非法输入返回空串，不抛错', () => {
    expect(localInputToIso('not-a-date')).toBe('')
  })

  it('本地分钟精度时间转为对应的 UTC ISO 串', () => {
    const local = '2026-11-07T14:22'
    expect(localInputToIso(local)).toBe(new Date(local).toISOString())
  })
})

describe('isoToLocalInput', () => {
  it('空串返回空串', () => {
    expect(isoToLocalInput('')).toBe('')
  })

  it('非法输入返回空串', () => {
    expect(isoToLocalInput('garbage')).toBe('')
  })

  it('ISO 串转为 datetime-local 控件格式（本地时区、分钟精度、补零）', () => {
    const iso = new Date(2026, 0, 5, 3, 7).toISOString()
    expect(isoToLocalInput(iso)).toBe('2026-01-05T03:07')
  })

  it('与 localInputToIso 互逆', () => {
    const local = '2026-11-07T09:05'
    expect(isoToLocalInput(localInputToIso(local))).toBe(local)
  })
})

describe('syncIsoWithLocalInput', () => {
  it('控件值未变时原样保留原 ISO（不丢秒级精度）', () => {
    const prev = new Date(2026, 10, 7, 9, 5, 42).toISOString()
    expect(syncIsoWithLocalInput(prev, isoToLocalInput(prev))).toBe(prev)
  })

  it('控件值变化时换算为新 ISO', () => {
    const prev = new Date(2026, 10, 7, 9, 5, 42).toISOString()
    expect(syncIsoWithLocalInput(prev, '2026-11-08T10:00')).toBe(localInputToIso('2026-11-08T10:00'))
  })

  it('清空控件即清空边界', () => {
    expect(syncIsoWithLocalInput(new Date().toISOString(), '')).toBe('')
  })
})
