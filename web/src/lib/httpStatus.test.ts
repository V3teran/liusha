import { describe, expect, it } from 'vitest'
import { methodColor, statusColor } from './httpStatus'

describe('statusColor', () => {
  it('2xx → sev-low', () => {
    expect(statusColor(200)).toBe('var(--sev-low)')
    expect(statusColor(204)).toBe('var(--sev-low)')
  })

  it('3xx → accent', () => {
    expect(statusColor(301)).toBe('var(--accent)')
    expect(statusColor(304)).toBe('var(--accent)')
  })

  it('4xx → sev-medium', () => {
    expect(statusColor(404)).toBe('var(--sev-medium)')
    expect(statusColor(429)).toBe('var(--sev-medium)')
  })

  it('5xx → sev-critical', () => {
    expect(statusColor(500)).toBe('var(--sev-critical)')
    expect(statusColor(503)).toBe('var(--sev-critical)')
  })

  it('区间外（含 0/未知）→ muted', () => {
    expect(statusColor(0)).toBe('var(--muted)')
    expect(statusColor(100)).toBe('var(--muted)')
    expect(statusColor(199)).toBe('var(--muted)')
  })
})

describe('methodColor', () => {
  it('DELETE → sev-critical', () => {
    expect(methodColor('DELETE')).toBe('var(--sev-critical)')
    expect(methodColor('delete')).toBe('var(--sev-critical)') // 大小写无关
  })

  it('写方法 POST/PUT/PATCH → accent', () => {
    expect(methodColor('POST')).toBe('var(--accent)')
    expect(methodColor('put')).toBe('var(--accent)')
    expect(methodColor('Patch')).toBe('var(--accent)')
  })

  it('读方法及其它 → muted', () => {
    expect(methodColor('GET')).toBe('var(--muted)')
    expect(methodColor('HEAD')).toBe('var(--muted)')
    expect(methodColor('OPTIONS')).toBe('var(--muted)')
  })
})
