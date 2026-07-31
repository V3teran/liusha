import type { AttackGraph, AttackGraphNode } from '@/api/types'

export interface GraphNodeData {
  id: string
  // task / hypothesis / probe / signal / finding / collapsed（占位）
  kind: string
  label: string
  status?: string
  severity?: string
  // provenance：derived(确定性) / agent(自标) / llm(提炼)——渲染层据此标注可信度。
  provenance?: string
  agent?: string
  dim: boolean
  collapsed: boolean
  expanded?: boolean
  // opening：折叠段是否为开场段（anchor=''，即侦察与初始访问）。collapsed 节点专用——
  // 渲染层（nodes.tsx）据此选图标（指北针=开场 / 放大镜=其余探索），不在这里拼 emoji 字符串。
  opening?: boolean
  // hiddenCount：该折叠段隐藏的步数（来自后端 collapsed 元数据，前端不再自算）。
  hiddenCount?: number
  anchor?: string
  chained: boolean
  // React Flow 的 Node<T> 要求 data 满足 Record<string, unknown>（索引签名）。
  [key: string]: unknown
}

export interface GraphEdgeData {
  id: string
  source: string
  target: string
  // spawns / pursues / tests / reveals / informs / confirms / depends_on
  type: string
}

export interface TransformedGraph {
  nodes: GraphNodeData[]
  edges: GraphEdgeData[]
}

// 占位节点 id 前缀（折叠段 → 可展开占位）。anchor='' 是开场段（挂在根前）。
export function placeholderId(anchor: string): string {
  return anchor === '' ? '__ph_root__' : `__ph_${anchor}`
}

export function anchorFromPlaceholderId(id: string): string {
  return id === '__ph_root__' ? '' : id.slice('__ph_'.length)
}

export function isPlaceholderId(id: string): boolean {
  return id.startsWith('__ph_')
}

/**
 * toGraphData 把执行图转成渲染层通用格式（不含布局坐标）。
 *
 * collapse=true（成果优先，默认）时只展开「成果路径」——后端 markOnPath 标好的 on_path 主干
 * （任务→判断→探测→信号→漏洞这条通向漏洞的线）；其余探索/死路节点按后端 collapsed 元数据
 * 折叠成占位段（"N 步已折叠"），点击展开。
 *
 * 关键：折叠决策全部信后端——可见性看 node.on_path，折叠段的锚点/步数看 g.collapsed。
 * 前端不再用旧版那套「finding||agent||on_path||error-action」独立启发式重算一遍 kept/hiddenCount
 * （那是与后端 on_path 口径不一致的第二套折叠逻辑，改一边不知会不会串味）。
 * 前端唯一保留的推导是「某折叠节点归属哪个可见锚点」（nearestOnPathAnchor），因为该归属关系
 * 未随 collapsed 元数据序列化下来，且算法与后端 nearestOnPathAncestor 完全一致（同样只认 on_path）。
 */
export function toGraphData(g: AttackGraph | null, collapse: boolean, expandedAnchors: Set<string>): TransformedGraph {
  if (!g) return { nodes: [], edges: [] }
  const byId = new Map<string, AttackGraphNode>()
  for (const n of g.nodes) byId.set(n.id, n)

  // 组合漏洞：有 depends_on 入边的 finding（前置漏洞 A+B 串成的更高危漏洞）→ 视觉强化。
  const chained = new Set(g.edges.filter((e) => e.type === 'depends_on').map((e) => e.to))

  const nodes: GraphNodeData[] = []
  const edges: GraphEdgeData[] = []
  const seenEdge = new Set<string>()
  let i = 0
  const addEdge = (src: string, tgt: string, type: string) => {
    if (!src || !tgt || src === tgt) return
    const k = `${src}->${tgt}:${type}`
    if (seenEdge.has(k)) return
    seenEdge.add(k)
    edges.push({ id: `e${i++}`, source: src, target: tgt, type })
  }
  const toData = (n: AttackGraphNode, dim: boolean): GraphNodeData => ({
    id: n.id,
    kind: n.kind,
    label: n.title,
    status: n.status,
    severity: n.severity,
    provenance: n.provenance,
    agent: n.agent,
    dim,
    collapsed: false,
    chained: chained.has(n.id),
  })

  // 完整模式：全节点（探索/死路 dim 降权）+ 原边，不折叠。
  if (!collapse) {
    for (const n of g.nodes) nodes.push(toData(n, n.on_path !== true))
    for (const e of g.edges) addEdge(e.from, e.to, e.type)
    return { nodes, edges }
  }

  // 成果优先：on_path 主干可见；其余节点折进后端给的 collapsed 段，展开时才可见。
  //
  // nearestOnPathAnchor：沿 parent_id 上溯到首个 on_path 节点 id（无则 ''=开场段）。
  // 镜像后端 nearestOnPathAncestor——同样只认 on_path，不是旧版那套独立启发式。
  const nearestOnPathAnchor = (id: string): string => {
    let p = byId.get(id)?.parent_id ?? ''
    const seen = new Set<string>()
    while (p && !seen.has(p)) {
      const pn = byId.get(p)
      if (!pn) return '' // 脏父指针 → 归开场段
      if (pn.on_path) return p
      seen.add(p)
      p = pn.parent_id ?? ''
    }
    return ''
  }
  const isVisible = (n: AttackGraphNode): boolean => n.on_path === true || expandedAnchors.has(nearestOnPathAnchor(n.id))
  const visibleId = (id: string): boolean => {
    const n = byId.get(id)
    return !!n && isVisible(n)
  }

  for (const n of g.nodes) {
    if (isVisible(n)) nodes.push(toData(n, false))
  }

  // 折叠段占位：完全由后端 collapsed 元数据驱动（锚点、步数、是否开场段），前端不自算。
  for (const seg of g.collapsed ?? []) {
    const isExpanded = expandedAnchors.has(seg.anchor)
    // label 只留纯文本（展开态/开场段/探索段）；交互态与段类型交给渲染层用结构化图标 + aria 表达。
    const label = isExpanded
      ? `收起 ${seg.hidden_count} 步`
      : seg.opening
        ? `侦察与初始访问 · ${seg.hidden_count} 步`
        : `探索 ${seg.hidden_count} 步`
    nodes.push({
      id: placeholderId(seg.anchor),
      kind: 'collapsed',
      label,
      dim: false,
      collapsed: true,
      expanded: isExpanded,
      opening: seg.opening,
      hiddenCount: seg.hidden_count,
      anchor: seg.anchor,
      chained: false,
    })
  }

  // 骨干边（parent_id 派生）：可见节点连到可见父。on_path 主干连续（markOnPath 从 finding
  // 上溯整条标记），故可见节点的父恒可见——不存在「父隐藏而子可见」需重路由的情况。
  const edgeType = new Map<string, string>()
  for (const e of g.edges) edgeType.set(`${e.from}->${e.to}`, e.type)
  for (const n of g.nodes) {
    if (!isVisible(n)) continue
    const p = n.parent_id ?? ''
    if (p && visibleId(p)) addEdge(p, n.id, edgeType.get(`${p}->${n.id}`) ?? 'tests')
  }
  // 占位挂在锚点下（开场段 anchor='' 无锚点 → 占位自然成图根）。
  for (const seg of g.collapsed ?? []) {
    if (seg.anchor !== '') addEdge(seg.anchor, placeholderId(seg.anchor), 'tests')
  }
  // 跨边（depends_on 组合链等非 parent_id 边）：两端可见才连。
  for (const e of g.edges) {
    if (byId.get(e.to)?.parent_id === e.from) continue // 已在骨干循环里画过
    if (visibleId(e.from) && visibleId(e.to)) addEdge(e.from, e.to, e.type)
  }
  return { nodes, edges }
}
