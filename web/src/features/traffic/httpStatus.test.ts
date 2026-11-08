import { describe, expect, it } from 'vitest'
import { methodColor, statusColor } from './httpStatus'

describe('statusColor', () => {
  it('2xx 成功 → status-success（绿）', () => {
    expect(statusColor(200)).toBe('var(--status-success)')
    expect(statusColor(204)).toBe('var(--status-success)')
  })

  it('3xx 跳转 → status-neutral（中性）', () => {
    expect(statusColor(301)).toBe('var(--status-neutral)')
    expect(statusColor(304)).toBe('var(--status-neutral)')
  })

  it('4xx → sev-medium', () => {
    expect(statusColor(404)).toBe('var(--sev-medium)')
    expect(statusColor(429)).toBe('var(--sev-medium)')
  })

  it('5xx → sev-critical', () => {
    expect(statusColor(500)).toBe('var(--sev-critical)')
    expect(statusColor(503)).toBe('var(--sev-critical)')
  })

  it('区间外（含 0/未知）→ status-neutral', () => {
    expect(statusColor(0)).toBe('var(--status-neutral)')
    expect(statusColor(100)).toBe('var(--status-neutral)')
    expect(statusColor(199)).toBe('var(--status-neutral)')
  })
})

describe('methodColor', () => {
  it('DELETE → sev-critical', () => {
    expect(methodColor('DELETE')).toBe('var(--sev-critical)')
    expect(methodColor('delete')).toBe('var(--sev-critical)') // 大小写无关
  })

  it('写方法 POST/PUT/PATCH → sev-info（蓝）', () => {
    expect(methodColor('POST')).toBe('var(--sev-info)')
    expect(methodColor('put')).toBe('var(--sev-info)')
    expect(methodColor('Patch')).toBe('var(--sev-info)')
  })

  it('读方法及其它 → status-neutral', () => {
    expect(methodColor('GET')).toBe('var(--status-neutral)')
    expect(methodColor('HEAD')).toBe('var(--status-neutral)')
    expect(methodColor('OPTIONS')).toBe('var(--status-neutral)')
  })
})
