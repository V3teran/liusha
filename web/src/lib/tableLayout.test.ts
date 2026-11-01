import { describe, expect, it } from 'vitest'
import { columnPixelWidths, totalColumnWidth } from './tableLayout'

describe('columnPixelWidths', () => {
  it('size 即设计像素宽度，逐列输出 px', () => {
    expect(columnPixelWidths([150, 110, 420])).toEqual(['150px', '110px', '420px'])
  })

  it('0 宽度钳到 1px（table-fixed 下 0 宽列内容不可见）', () => {
    expect(columnPixelWidths([0, 80])).toEqual(['1px', '80px'])
  })

  it('空数组返回空数组', () => {
    expect(columnPixelWidths([])).toEqual([])
  })
})

describe('totalColumnWidth', () => {
  it('总和 = 表格最小宽度（容器窄于此值出横向滚动）', () => {
    expect(totalColumnWidth([150, 110, 420])).toBe(680)
  })

  it('0 宽度按钳值 1 计入总和', () => {
    expect(totalColumnWidth([0, 80])).toBe(81)
  })

  it('空数组总和为 0（表格随容器铺满，无横向滚动）', () => {
    expect(totalColumnWidth([])).toBe(0)
  })
})
