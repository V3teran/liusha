import { describe, expect, it } from 'vitest'
import { columnWidthPercents } from './tableLayout'

describe('columnWidthPercents', () => {
  it('按权重算出等比例百分比，总和为 100%', () => {
    const pcts = columnWidthPercents([1, 1, 2])
    expect(pcts).toEqual(['25.000%', '25.000%', '50.000%'])
  })

  it('单列权重占满 100%', () => {
    expect(columnWidthPercents([42])).toEqual(['100.000%'])
  })

  it('全部权重为 0（总和为 0）时退化为 auto，不产生 NaN%', () => {
    expect(columnWidthPercents([0, 0])).toEqual(['auto', 'auto'])
  })

  it('空数组返回空数组', () => {
    expect(columnWidthPercents([])).toEqual([])
  })
})
