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
    kind: 'hypothesis',
    label: '标签',
    dim: false,
    collapsed: false,
    chained: false,
    ...overrides,
  }
}

describe('AttackGraphNode', () => {
  it('hypothesis(判断) 节点渲染圆形（circle）', () => {
    const { container } = renderNode(baseData({ kind: 'hypothesis' }))
    expect(container.querySelector('circle')).toBeTruthy()
    expect(container.querySelector('polygon')).toBeNull()
  })

  it('probe(探测) 节点渲染圆形（circle）', () => {
    const { container } = renderNode(baseData({ kind: 'probe' }))
    expect(container.querySelector('circle')).toBeTruthy()
    expect(container.querySelector('polygon')).toBeNull()
  })

  it('task(任务) 节点渲染六边形（polygon，6 个顶点）', () => {
    const { container } = renderNode(baseData({ kind: 'task' }))
    const polygon = container.querySelector('polygon')
    expect(polygon).toBeTruthy()
    const points = polygon!.getAttribute('points')!.trim().split(/\s+/)
    expect(points).toHaveLength(6)
  })

  it('signal(信号) 节点渲染菱形（polygon，4 个顶点）', () => {
    const { container } = renderNode(baseData({ kind: 'signal' }))
    const polygon = container.querySelector('polygon')
    expect(polygon).toBeTruthy()
    const points = polygon!.getAttribute('points')!.trim().split(/\s+/)
    expect(points).toHaveLength(4)
  })

  it('finding(漏洞) 节点渲染星形（polygon，10 个顶点）', () => {
    const { container } = renderNode(baseData({ kind: 'finding', severity: 'high' }))
    const polygon = container.querySelector('polygon')
    expect(polygon).toBeTruthy()
    const points = polygon!.getAttribute('points')!.trim().split(/\s+/)
    expect(points).toHaveLength(10)
  })

  it('provenance=llm 的节点用虚线描边（标注可信度低于 derived/agent）', () => {
    const { container } = renderNode(baseData({ kind: 'hypothesis', provenance: 'llm' }))
    expect(container.querySelector('circle')?.getAttribute('stroke-dasharray')).toBe('3,3')
  })

  it('provenance=agent 的节点用实线描边（无 dasharray）', () => {
    const { container } = renderNode(baseData({ kind: 'hypothesis', provenance: 'agent' }))
    expect(container.querySelector('circle')?.getAttribute('stroke-dasharray')).toBeNull()
  })

  it('collapsed 节点渲染徽标与标签，不含 polygon/circle', () => {
    const { container, getByText } = renderNode(baseData({ kind: 'collapsed', label: '折叠段' }))
    expect(container.querySelector('polygon')).toBeNull()
    expect(getByText('折叠段')).toBeTruthy()
  })

  it('collapsed 节点 expanded=true 时徽标为实心背景 + 实线边框 + chevron-down 图标', () => {
    const { container } = renderNode(baseData({ kind: 'collapsed', expanded: true }))
    const badge = container.querySelector('div[style]')
    expect(badge?.getAttribute('style')).toContain('--graph-task')
    expect(badge?.getAttribute('style')).not.toContain('background: transparent')
    expect(badge?.getAttribute('style')).toContain('border-style: solid')
    // chevron-down 是 lucide 图标，渲染为 svg；折叠占位节点应恰好 2 个 svg（段类型图标 + toggle 图标）。
    expect(container.querySelectorAll('svg')).toHaveLength(2)
  })

  it('collapsed 节点 expanded=false 时透明背景 + 虚线边框 + chevron-right 图标', () => {
    const { container } = renderNode(baseData({ kind: 'collapsed', expanded: false }))
    const badge = container.querySelector('div[style]')
    expect(badge?.getAttribute('style')).toContain('background: transparent')
    expect(badge?.getAttribute('style')).toContain('border-style: dashed')
  })

  it('collapsed 节点 opening=true 时用侦察图标（Compass），否则用探索图标（Search）', () => {
    const { container: openingContainer } = renderNode(baseData({ kind: 'collapsed', opening: true }))
    const { container: exploringContainer } = renderNode(baseData({ kind: 'collapsed', opening: false }))
    // 两者 DOM 结构不同（不同图标组件渲染不同 path），断言不完全相等即可证明确实按 opening 切换。
    expect(openingContainer.innerHTML).not.toBe(exploringContainer.innerHTML)
  })

  it('chained finding 节点比普通 finding 更大，且描边为金色', () => {
    const normal = renderNode(baseData({ kind: 'finding', chained: false }))
    const normalSvg = normal.container.querySelector('svg')
    const normalSize = Number(normalSvg?.getAttribute('width'))

    const chained = renderNode(baseData({ kind: 'finding', chained: true }))
    const chainedSvg = chained.container.querySelector('svg')
    const chainedSize = Number(chainedSvg?.getAttribute('width'))
    const polygon = chained.container.querySelector('polygon')

    expect(chainedSize).toBeGreaterThan(normalSize)
    expect(polygon?.getAttribute('stroke')).toBe('var(--graph-depends-on)')
    expect(polygon?.getAttribute('stroke-width')).toBe('3')
  })

  it('data.dim 为 true 时外层容器 opacity 降低', () => {
    const { container } = renderNode(baseData({ kind: 'probe', dim: true }))
    const wrapper = container.querySelector('div[style]')
    expect(wrapper?.getAttribute('style')).toContain('opacity: 0.28')
  })

  it('data.dim 为 false 时外层容器完全不透明', () => {
    const { container } = renderNode(baseData({ kind: 'probe', dim: false }))
    const wrapper = container.querySelector('div[style]')
    expect(wrapper?.getAttribute('style')).toContain('opacity: 1')
  })

  it('status 为 failed 的 probe 节点使用死路灰配色描边', () => {
    const { container } = renderNode(baseData({ kind: 'probe', status: 'failed' }))
    const circle = container.querySelector('circle')
    expect(circle?.getAttribute('stroke')).toBe('var(--faint)')
  })

  it('status 为 refuted 的 hypothesis 节点也判死路（灰描边）', () => {
    const { container } = renderNode(baseData({ kind: 'hypothesis', status: 'refuted' }))
    const circle = container.querySelector('circle')
    expect(circle?.getAttribute('stroke')).toBe('var(--faint)')
  })

  it('渲染节点标签文本', () => {
    const { getByText } = renderNode(baseData({ label: '自定义标签' }))
    expect(getByText('自定义标签')).toBeTruthy()
  })

  it('标签截断到 2 行（line-clamp-2），与 layout.ts 的 LABEL_MAX_LINES 对齐；title 属性保留全文', () => {
    const longLabel = '这是一段很长很长很长很长很长很长很长很长很长的节点标签用来测试换行截断行为'
    const { getByText } = renderNode(baseData({ kind: 'probe', label: longLabel }))
    const span = getByText(longLabel)
    expect(span.className).toContain('line-clamp-2')
    expect(span.getAttribute('title')).toBe(longLabel)
  })
})
