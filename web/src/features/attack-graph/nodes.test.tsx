import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { ReactFlowProvider } from '@xyflow/react'
import { AttackGraphNode } from './nodes'
import type { GraphNodeData } from './graphTransform'

// NodeProps 字段很多，测试只需 data（组件本身只用 data），其余用 as any 补齐。
function renderNode(data: GraphNodeData) {
  return render(
    <ReactFlowProvider>
      <AttackGraphNode
        {...({
          id: 'n1',
          data,
          selected: false,
          type: 'attackGraph',
          dragging: false,
          zIndex: 0,
          isConnectable: true,
          positionAbsoluteX: 0,
          positionAbsoluteY: 0,
        } as any)}
      />
    </ReactFlowProvider>,
  )
}

function baseData(overrides: Partial<GraphNodeData> = {}): GraphNodeData {
  return {
    id: 'n1',
    seq: 1,
    kind: 'asset',
    label: '标签',
    confidence: 'confirmed',
    ...overrides,
  }
}

describe('AttackGraphNode', () => {
  it('asset(资产) 节点渲染圆形（circle）', () => {
    const { container } = renderNode(baseData({ kind: 'asset' }))
    expect(container.querySelector('circle')).toBeTruthy()
    expect(container.querySelector('polygon')).toBeNull()
  })

  it('target(目标) 节点渲染六边形（polygon，6 个顶点）', () => {
    const { container } = renderNode(baseData({ kind: 'target' }))
    const polygon = container.querySelector('polygon')
    expect(polygon).toBeTruthy()
    const points = polygon!.getAttribute('points')!.trim().split(/\s+/)
    expect(points).toHaveLength(6)
  })

  it('credential(凭据) 节点渲染菱形（polygon，4 个顶点）', () => {
    const { container } = renderNode(baseData({ kind: 'credential' }))
    const polygon = container.querySelector('polygon')
    expect(polygon).toBeTruthy()
    const points = polygon!.getAttribute('points')!.trim().split(/\s+/)
    expect(points).toHaveLength(4)
  })

  it('access(立足点) 节点渲染方形（rect）', () => {
    const { container } = renderNode(baseData({ kind: 'access' }))
    expect(container.querySelector('rect')).toBeTruthy()
    expect(container.querySelector('polygon')).toBeNull()
    expect(container.querySelector('circle')).toBeNull()
  })

  it('finding(漏洞) 节点渲染星形（polygon，10 个顶点）', () => {
    const { container } = renderNode(baseData({ kind: 'finding', severity: 'high' }))
    const polygon = container.querySelector('polygon')
    expect(polygon).toBeTruthy()
    const points = polygon!.getAttribute('points')!.trim().split(/\s+/)
    expect(points).toHaveLength(10)
  })

  it('confidence=assumed 的节点用虚线描边（未经 Verifier 坐实）', () => {
    const { container } = renderNode(baseData({ kind: 'asset', confidence: 'assumed' }))
    expect(container.querySelector('circle')?.getAttribute('stroke-dasharray')).toBe('3,3')
  })

  it('confidence=confirmed 的节点用实线描边（无 dasharray）', () => {
    const { container } = renderNode(baseData({ kind: 'asset', confidence: 'confirmed' }))
    expect(container.querySelector('circle')?.getAttribute('stroke-dasharray')).toBeNull()
  })

  it('finding 节点比资产节点大（星形是攻击链终点视觉锚）', () => {
    const asset = renderNode(baseData({ kind: 'asset' }))
    const assetSize = Number(asset.container.querySelector('svg')?.getAttribute('width'))
    const finding = renderNode(baseData({ kind: 'finding', severity: 'high' }))
    const findingSize = Number(finding.container.querySelector('svg')?.getAttribute('width'))
    expect(findingSize).toBeGreaterThan(assetSize)
  })

  it('渲染节点标签文本', () => {
    const { getByText } = renderNode(baseData({ label: '自定义标签' }))
    expect(getByText('自定义标签')).toBeTruthy()
  })

  it('标签截断到 2 行（line-clamp-2），与 layout.ts 的 LABEL_MAX_LINES 对齐；title 属性保留全文', () => {
    const longLabel = '这是一段很长很长很长很长很长很长很长很长很长的节点标签用来测试换行截断行为'
    const { getByText } = renderNode(baseData({ kind: 'asset', label: longLabel }))
    const span = getByText(longLabel)
    expect(span.className).toContain('line-clamp-2')
    expect(span.getAttribute('title')).toBe(longLabel)
  })
})
