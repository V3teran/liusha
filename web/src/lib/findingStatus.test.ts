import { describe, it, expect } from 'vitest'
import { findingStatusMeta, FINDING_STATUS_OPTIONS } from './findingStatus'

describe('findingStatusMeta', () => {
  it('五态各返回正确中文标签', () => {
    expect(findingStatusMeta('open').label).toBe('待处理')
    expect(findingStatusMeta('confirmed').label).toBe('已确认')
    expect(findingStatusMeta('fixed').label).toBe('已修复')
    expect(findingStatusMeta('false_positive').label).toBe('误报')
    expect(findingStatusMeta('accepted').label).toBe('接受风险')
  })

  it('未知/空状态回落到 open（待处理）', () => {
    expect(findingStatusMeta('bogus').key).toBe('open')
    expect(findingStatusMeta(undefined).key).toBe('open')
  })

  it('每态都有非空配色', () => {
    for (const o of FINDING_STATUS_OPTIONS) {
      expect(findingStatusMeta(o.value).color).toMatch(/^#[0-9a-f]{6}$/i)
    }
  })

  it('下拉选项覆盖全部五态', () => {
    expect(FINDING_STATUS_OPTIONS.map((o) => o.value)).toEqual([
      'open',
      'confirmed',
      'fixed',
      'false_positive',
      'accepted',
    ])
  })
})
