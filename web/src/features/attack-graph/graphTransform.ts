import type { AttackGraph, AttackGraphNode } from '@/api/types'

export interface GraphNodeData {
  id: string
  seq: number
  // target / asset / credential / access / finding
  kind: string
  label: string
  // assumed 节点用虚线描边（未经 Verifier 坐实，可信度低于 confirmed）。
  confidence: string
  // finding 节点配色用（attrs.severity）。
  severity?: string
  // React Flow 的 Node<T> 要求 data 满足 Record<string, unknown>（索引签名）。
  [key: string]: unknown
}

export interface GraphEdgeData {
  id: string
  source: string
  target: string
  // derives / enables / on
  rel: string
}

export interface TransformedGraph {
  nodes: GraphNodeData[]
  edges: GraphEdgeData[]
}

// str 从 attrs 安全取字符串字段（attrs 形状由 kind 定，缺字段返回空串）。
function str(attrs: Record<string, unknown>, key: string): string {
  const v = attrs?.[key]
  return typeof v === 'string' ? v : ''
}

// nodeLabel 为各类节点选一个短标签：finding 用漏洞摘要，其余用 ref.locator（域内寻址）
// 兜底到 kind——世界模型节点本就带稳定的多态标识，不必像旧思维链那样拼推理文本。
export function nodeLabel(n: AttackGraphNode): string {
  if (n.kind === 'finding') {
    return str(n.attrs, 'summary') || n.ref.locator || 'finding'
  }
  return n.ref.locator || str(n.attrs, 'service') || n.kind
}

/**
 * toGraphData 把攻击图（世界模型投影）转成渲染层通用格式（不含布局坐标）。
 *
 * 与旧执行图不同：世界模型节点都是 Verifier 坐实/明确假定的真相，无「探索噪声」可折叠，
 * 故不再有 collapse / on_path / provenance 三套启发式——全节点全边直出，assumed 用虚线区分。
 */
export function toGraphData(g: AttackGraph | null): TransformedGraph {
  if (!g) return { nodes: [], edges: [] }

  const nodes: GraphNodeData[] = g.nodes.map((n) => ({
    id: n.id,
    seq: n.seq,
    kind: n.kind,
    label: nodeLabel(n),
    confidence: n.confidence,
    severity: n.kind === 'finding' ? str(n.attrs, 'severity') : undefined,
  }))

  const seen = new Set<string>()
  const edges: GraphEdgeData[] = []
  for (const e of g.edges) {
    if (!e.source || !e.target || e.source === e.target) continue
    const k = `${e.source}->${e.target}:${e.rel}`
    if (seen.has(k)) continue
    seen.add(k)
    edges.push({ id: e.id || `e${edges.length}`, source: e.source, target: e.target, rel: e.rel })
  }

  return { nodes, edges }
}
