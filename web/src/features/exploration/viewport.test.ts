import { describe, expect, it } from 'vitest'
import type { ExplorationFlowNode } from './layout'
import { CHAIN_MARGIN, DETAIL_RESERVE, anyOutside, detailReserve, frameNodes } from './viewport'

// 两个并排的动作卡片：264 宽，间隔 96，整组 624 × 104
const nodes = [
  { id: 'a', position: { x: 0, y: 0 }, width: 264, height: 104 },
  { id: 'b', position: { x: 360, y: 0 }, width: 264, height: 104 },
] as unknown as ExplorationFlowNode[]

describe('探索图视口', () => {
  it('detailReserve：画布够宽时给详情浮层留位，窄屏上不留', () => {
    expect(detailReserve(1440)).toBe(DETAIL_RESERVE)
    expect(detailReserve(DETAIL_RESERVE + 360)).toBe(DETAIL_RESERVE)
    expect(detailReserve(DETAIL_RESERVE + 359)).toBe(0)
    expect(detailReserve(375)).toBe(0)
  })

  it('frameNodes：装得下就不放大，整组贴着留白、垂直居中', () => {
    expect(frameNodes(nodes, 1000, 704)).toEqual({ x: CHAIN_MARGIN, y: CHAIN_MARGIN + (704 - 104) / 2, zoom: 1 })
  })

  it('frameNodes：装不下就等比缩小到整组装得下', () => {
    // 可用宽 312 放不下 624：缩到一半
    expect(frameNodes(nodes, 312, 400)).toEqual({ x: CHAIN_MARGIN, y: CHAIN_MARGIN + (400 - 52) / 2, zoom: 0.5 })
  })

  it('frameNodes：没有节点或没有可用空间时不给视口', () => {
    expect(frameNodes([], 1000, 704)).toBeNull()
    expect(frameNodes(nodes, 0, 704)).toBeNull()
  })

  it('anyOutside：右缘扣掉详情浮层；有节点被浮层压住、出界或不完整就成立', () => {
    const full = { x: 0, y: 0, zoom: 1 }
    // 画布 1024 宽：右缘在 624。两个卡片右缘正好 624——完整在视野里
    expect(anyOutside(nodes, full, 1024, 800, DETAIL_RESERVE)).toBe(false)
    // 画布窄 1px：第二个卡片被压住一点
    expect(anyOutside(nodes, full, 1023, 800, DETAIL_RESERVE)).toBe(true)
    // 不留位时两个都在
    expect(anyOutside(nodes, full, 1023, 800, 0)).toBe(false)
    // 左边出界
    expect(anyOutside(nodes, { x: -1, y: 0, zoom: 1 }, 1024, 800, DETAIL_RESERVE)).toBe(true)
    // 下边出界
    expect(anyOutside(nodes, { x: 0, y: 700, zoom: 1 }, 1024, 800, DETAIL_RESERVE)).toBe(true)
    // 缩小后都在
    expect(anyOutside(nodes, { x: 0, y: 0, zoom: 0.5 }, 1024, 800, DETAIL_RESERVE)).toBe(false)
  })
})
