/**
 * 探索图的视口计算（纯函数）：判断一组节点是否已经在视野里，以及把它们收进视野的视口。
 * 画布组件用来在做「来路适配」——点选节点后，它的来路有节点掉出视野时才移动视口。
 */

import { getNodesBounds, type Viewport } from '@xyflow/react'
import { NODE_HEIGHT, NODE_WIDTH, type ExplorationFlowNode } from './layout'

/** 来路四周的留白（px，屏幕坐标） */
export const CHAIN_MARGIN = 40
/** 详情浮层（w-96 = 384px）连同右侧留白占掉的宽度：它盖在画布右缘，来路要收在它左边 */
export const DETAIL_RESERVE = 400
/** 扣掉详情浮层后剩得比这还窄时不再给它留位：窄屏上它本来就盖住大半个画布 */
export const MIN_FREE_WIDTH = 360

/** 画布右缘有多少宽度被详情浮层占着（窄屏上它盖住的是整块画布，不单独留位）。 */
export function detailReserve(canvasWidth: number): number {
  return canvasWidth - DETAIL_RESERVE >= MIN_FREE_WIDTH ? DETAIL_RESERVE : 0
}

/** 节点在屏幕上的外框：左、上、宽、高。 */
function screenBox(n: ExplorationFlowNode, viewport: Viewport) {
  return {
    left: viewport.x + n.position.x * viewport.zoom,
    top: viewport.y + n.position.y * viewport.zoom,
    width: NODE_WIDTH * viewport.zoom,
    height: NODE_HEIGHT * viewport.zoom,
  }
}

/** 一组节点里有没有任何一个不完整地落在视野里（视野的右缘扣掉 reserve）。 */
export function anyOutside(
  nodes: ExplorationFlowNode[],
  viewport: Viewport,
  canvasWidth: number,
  canvasHeight: number,
  reserve: number,
): boolean {
  const rightEdge = canvasWidth - reserve
  return nodes.some((n) => {
    const box = screenBox(n, viewport)
    return box.left < 0 || box.left + box.width > rightEdge || box.top < 0 || box.top + box.height > canvasHeight
  })
}

/**
 * 把一组节点收进一块可用区域（宽 availWidth、高 availHeight）的视口：整组靠左上对齐留白、
 * 垂直居中，只缩小不放大。节点是纵向排开的一列时靠左，读图时视线不必先横跨一段空白。
 */
export function frameNodes(
  nodes: ExplorationFlowNode[],
  availWidth: number,
  availHeight: number,
): Viewport | null {
  if (nodes.length === 0 || availWidth <= 0 || availHeight <= 0) return null
  const bounds = getNodesBounds(nodes)
  const zoom = Math.min(availWidth / bounds.width, availHeight / bounds.height, 1)
  return {
    x: CHAIN_MARGIN - bounds.x * zoom,
    y: CHAIN_MARGIN + (availHeight - bounds.height * zoom) / 2 - bounds.y * zoom,
    zoom,
  }
}
