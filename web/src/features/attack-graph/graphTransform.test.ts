import { describe, expect, it } from 'vitest'
import type { AttackGraph } from '@/api/types'
import { anchorFromPlaceholderId, isPlaceholderId, placeholderId, toGraphData } from './graphTransform'

describe('placeholderId / anchorFromPlaceholderId / isPlaceholderId', () => {
  it('空 anchor 映射到根占位 id', () => {
    expect(placeholderId('')).toBe('__ph_root__')
    expect(anchorFromPlaceholderId('__ph_root__')).toBe('')
  })

  it('非空 anchor 映射到前缀占位 id', () => {
    expect(placeholderId('node1')).toBe('__ph_node1')
    expect(anchorFromPlaceholderId('__ph_node1')).toBe('node1')
  })

  it('isPlaceholderId 正确判断', () => {
    expect(isPlaceholderId('__ph_root__')).toBe(true)
    expect(isPlaceholderId('__ph_node1')).toBe(true)
    expect(isPlaceholderId('real-node-id')).toBe(false)
  })
})

// mkGraph 补齐 AttackGraph 必填字段（running/enriched），测试只关心 nodes/edges/collapsed。
function mkGraph(over: Partial<AttackGraph>): AttackGraph {
  return { task_id: 't', conversation_id: 'c', running: false, enriched: false, nodes: [], edges: [], ...over }
}

describe('toGraphData', () => {
  it('null 图返回空节点边', () => {
    expect(toGraphData(null, true, new Set())).toEqual({ nodes: [], edges: [] })
  })

  it('完整模式（collapse=false）：全节点保留，非 on_path 降权 dim', () => {
    const g = mkGraph({
      nodes: [
        { id: 'h', kind: 'hypothesis', title: '判断', on_path: true },
        { id: 'p', kind: 'probe', title: '探测', parent_id: 'h', on_path: false },
      ],
      edges: [{ from: 'h', to: 'p', type: 'tests' }],
    })
    const { nodes, edges } = toGraphData(g, false, new Set())
    expect(nodes).toHaveLength(2)
    expect(nodes.find((n) => n.id === 'h')?.dim).toBe(false)
    expect(nodes.find((n) => n.id === 'p')?.dim).toBe(true) // on_path !== true → dim
    expect(edges).toHaveLength(1)
    expect(edges[0]).toMatchObject({ source: 'h', target: 'p', type: 'tests' })
  })

  it('完整模式：自环边被跳过', () => {
    const g = mkGraph({
      nodes: [{ id: 'a', kind: 'probe', title: 'x', on_path: true }],
      edges: [{ from: 'a', to: 'a', type: 'tests' }],
    })
    const { edges } = toGraphData(g, false, new Set())
    expect(edges).toHaveLength(0)
  })

  it('成果优先模式：on_path 主干（任务→判断→探测→漏洞）全部保留', () => {
    // on_path 由后端 markOnPath 算：从 finding 沿 parent 上溯到根，整条路径标记。
    const g = mkGraph({
      nodes: [
        { id: 'task-root', kind: 'task', title: '主线', on_path: true },
        { id: 'h', kind: 'hypothesis', title: '判断', parent_id: 'task-root', on_path: true },
        { id: 'p', kind: 'probe', title: '探测', parent_id: 'h', on_path: true },
        { id: 'f', kind: 'finding', title: '漏洞', parent_id: 'p', on_path: true, severity: 'high' },
      ],
      edges: [
        { from: 'task-root', to: 'h', type: 'pursues' },
        { from: 'h', to: 'p', type: 'tests' },
        { from: 'p', to: 'f', type: 'confirms' },
      ],
    })
    const { nodes, edges } = toGraphData(g, true, new Set())
    expect(nodes.map((n) => n.id).sort()).toEqual(['f', 'h', 'p', 'task-root'])
    // 骨干边类型按后端给的 type 透传，不再一律标 flow。
    expect(edges.find((e) => e.source === 'p' && e.target === 'f')?.type).toBe('confirms')
    expect(edges.find((e) => e.source === 'task-root' && e.target === 'h')?.type).toBe('pursues')
  })

  it('成果优先模式：非 on_path 节点默认折叠（不出现在可见节点里），由后端 collapsed 元数据出占位', () => {
    const g = mkGraph({
      nodes: [
        { id: 'task-root', kind: 'task', title: '主线', on_path: true },
        { id: 'p1', kind: 'probe', title: '探测1', parent_id: 'task-root', on_path: true },
        { id: 'p2', kind: 'probe', title: '死路探测', parent_id: 'task-root', on_path: false },
        { id: 'f', kind: 'finding', title: '漏洞', parent_id: 'p1', on_path: true },
      ],
      edges: [],
      collapsed: [{ anchor: 'task-root', hidden_count: 1, opening: false }],
    })
    const { nodes } = toGraphData(g, true, new Set())
    expect(nodes.some((n) => n.id === 'p2')).toBe(false) // 死路探测折叠，不直接可见
    const placeholder = nodes.find((n) => n.kind === 'collapsed')
    expect(placeholder).toBeTruthy()
    expect(placeholder?.anchor).toBe('task-root')
    expect(placeholder?.label).toContain('探索')
    expect(placeholder?.label).toContain('1 步')
    expect(placeholder?.opening).toBe(false)
  })

  it('成果优先模式：开场段（anchor=空, opening=true）占位无父，label 为侦察与初始访问', () => {
    const g = mkGraph({
      nodes: [
        { id: 'task-root', kind: 'task', title: '主线', on_path: true },
        { id: 'p0', kind: 'probe', title: '开场探测', on_path: false },
        { id: 'f', kind: 'finding', title: '漏洞', parent_id: 'task-root', on_path: true },
      ],
      edges: [],
      collapsed: [{ anchor: '', hidden_count: 1, opening: true }],
    })
    const { nodes } = toGraphData(g, true, new Set())
    const placeholder = nodes.find((n) => n.kind === 'collapsed')
    expect(placeholder?.id).toBe('__ph_root__')
    expect(placeholder?.opening).toBe(true)
    expect(placeholder?.label).toContain('侦察与初始访问')
  })

  it('成果优先模式：展开折叠段后隐藏节点变可见 + 占位变收起态', () => {
    const g = mkGraph({
      nodes: [
        { id: 'task-root', kind: 'task', title: '主线', on_path: true },
        { id: 'p2', kind: 'probe', title: '死路探测', parent_id: 'task-root', on_path: false },
        { id: 'f', kind: 'finding', title: '漏洞', parent_id: 'task-root', on_path: true },
      ],
      edges: [],
      collapsed: [{ anchor: 'task-root', hidden_count: 1, opening: false }],
    })
    const { nodes } = toGraphData(g, true, new Set(['task-root']))
    expect(nodes.some((n) => n.id === 'p2')).toBe(true) // 展开后可见
    const placeholder = nodes.find((n) => n.kind === 'collapsed')
    expect(placeholder?.expanded).toBe(true)
    expect(placeholder?.label).toContain('收起')
  })

  it('成果优先模式：前端只信后端 on_path，不再用 evidence/confirms 边自己重算成果路径', () => {
    // 这条测试锚定"前端不再重新计算成果路径"的架构决策：p1 未被后端标 on_path，即便它有一条
    // confirms 边指向 finding，也该被折叠（旧实现会因为它是证据来源而强留）。
    const g = mkGraph({
      nodes: [
        { id: 'task-root', kind: 'task', title: '主线', on_path: true },
        { id: 'p1', kind: 'probe', title: '探测', parent_id: 'task-root', on_path: false },
        { id: 'f', kind: 'finding', title: '漏洞', on_path: true },
      ],
      edges: [{ from: 'p1', to: 'f', type: 'confirms' }],
      collapsed: [{ anchor: 'task-root', hidden_count: 1, opening: false }],
    })
    const { nodes } = toGraphData(g, true, new Set())
    expect(nodes.some((n) => n.id === 'p1')).toBe(false)
    expect(nodes.some((n) => n.kind === 'collapsed')).toBe(true)
  })

  it('组合漏洞（有 depends_on 入边）标记 chained=true', () => {
    const g = mkGraph({
      nodes: [
        { id: 'f1', kind: 'finding', title: '前置漏洞', on_path: true },
        { id: 'f2', kind: 'finding', title: '组合漏洞', on_path: true },
      ],
      edges: [{ from: 'f1', to: 'f2', type: 'depends_on' }],
    })
    const { nodes } = toGraphData(g, true, new Set())
    expect(nodes.find((n) => n.id === 'f2')?.chained).toBe(true)
    expect(nodes.find((n) => n.id === 'f1')?.chained).toBe(false)
  })

  it('成果优先模式：depends_on 跨边两端可见才连，且不重复添加', () => {
    const g = mkGraph({
      nodes: [
        { id: 'f1', kind: 'finding', title: 'a', on_path: true },
        { id: 'f2', kind: 'finding', title: 'b', on_path: true },
      ],
      edges: [
        { from: 'f1', to: 'f2', type: 'depends_on' },
        { from: 'f1', to: 'f2', type: 'depends_on' },
      ],
    })
    const { edges } = toGraphData(g, true, new Set())
    expect(edges).toHaveLength(1)
    expect(edges[0].type).toBe('depends_on')
  })

  it('provenance=llm 的节点透传 provenance 字段（供渲染层标注可信度）', () => {
    const g = mkGraph({
      nodes: [{ id: 'h', kind: 'hypothesis', title: 'LLM 提炼判断', on_path: true, provenance: 'llm' }],
      edges: [],
    })
    const { nodes } = toGraphData(g, true, new Set())
    expect(nodes.find((n) => n.id === 'h')?.provenance).toBe('llm')
  })
})
