import { useCallback, useEffect, useMemo, useState } from 'react'
import { ReactFlow, Background, Controls, MiniMap, type Edge } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { OwnerPicker } from '@/components/OwnerPicker'
import type { AttackGraphNode } from '@/api/types'
import { severityColor } from '@/lib/severity'
import { toGraphData, type GraphNodeData } from '@/features/attack-graph/graphTransform'
import { layoutGraph } from '@/features/attack-graph/layout'
import { nodeTypes, type AttackGraphNodeType } from '@/features/attack-graph/nodes'
import { useAttackGraphQuery } from '@/features/attack-graph/useAttackGraphQuery'
import { useColorMode } from '@/hooks/useColorMode'

// 5 类节点的中文短标签（详情面板徽标 + 图例）。
function kindLabel(k: string): string {
  switch (k) {
    case 'target':
      return '目标'
    case 'asset':
      return '资产'
    case 'credential':
      return '凭据'
    case 'access':
      return '立足点'
    default:
      return '漏洞'
  }
}

// 节点徽标底色（详情面板）：与 nodes.tsx 的 fillColor 同一套 --graph-* 语义。
function kindColor(k: string): string {
  switch (k) {
    case 'target':
      return 'var(--graph-target)'
    case 'asset':
      return 'var(--graph-asset)'
    case 'credential':
      return 'var(--graph-credential)'
    case 'access':
      return 'var(--graph-access)'
    default:
      return 'var(--sev-critical)'
  }
}

// edge 样式（3 类关系边）：enables 金(攻击链,最重要)、derives 青(认知因果)用醒目色；
// on(归属附着)走中性灰实线(结构骨架)。颜色用 index.css 的 --graph-* token（随主题切换）。
function edgeStyle(rel: string): { stroke: string; strokeWidth: number; strokeDasharray?: string } {
  if (rel === 'enables') return { stroke: 'var(--graph-enables)', strokeWidth: 2, strokeDasharray: '4,4' }
  if (rel === 'derives') return { stroke: 'var(--graph-derives)', strokeWidth: 1.5, strokeDasharray: '2,3' }
  return { stroke: 'var(--border)', strokeWidth: 1.5 }
}

// MiniMap 节点配色：与主图 fillColor 同一套语义，独立算一遍（不引入额外抽象）。
// nodeColor prop 按 React Flow 泛型 Node 声明，运行时对象即本图 AttackGraphNodeType，故转型为 GraphNodeData。
function minimapNodeColor(node: { data: Record<string, unknown> }): string {
  const d = node.data as unknown as GraphNodeData
  if (d.kind === 'finding') return severityColor[d.severity ?? ''] ?? 'var(--sev-low)'
  return kindColor(d.kind)
}

// attrs 里排除已单列展示的字段，其余键值对在详情面板补充展示。
function attrEntries(attrs: Record<string, unknown>): Array<[string, string]> {
  const skip = new Set(['summary', 'severity'])
  return Object.entries(attrs ?? {})
    .filter(([k, v]) => !skip.has(k) && v != null && v !== '')
    .map(([k, v]) => [k, typeof v === 'string' ? v : JSON.stringify(v)])
}

// 攻击图页：选 task → 后端按 task→assignment 解析 → 拉 L3 世界模型图 → React Flow 分层 DAG 渲染。
// 5 类节点：目标(target)/资产(asset)/凭据(credential)/立足点(access)/漏洞(finding)；
// 3 类边：on(归属,中性灰) / derives(认知因果,青) / enables(攻击链,金)。assumed 节点虚线描边。
// 布局 dagre（自上而下）；点节点弹详情。数据层见 useAttackGraphQuery（react-query + 轮询兜底）。
export function AttackGraphPage() {
  const [owner, setOwner] = useState('')
  const [selected, setSelected] = useState<AttackGraphNode | null>(null)
  const [live, setLive] = useState(true) // 实时开关：关闭则不轮询，只拉一次静态图
  const colorMode = useColorMode()

  const graphQuery = useAttackGraphQuery(owner, live)
  const data = graphQuery.data ?? null
  const nodes = data?.nodes ?? []
  const verifications = data?.verifications ?? []

  // 切 owner / 图变化时清掉选中态（旧图节点在新图里无意义）。
  const taskID = data?.scan_id ?? ''
  useEffect(() => {
    setSelected(null)
  }, [taskID, owner])

  // 图数据转换 + dagre 布局 → React Flow nodes/edges。
  const { flowNodes, flowEdges } = useMemo(() => {
    const transformed = toGraphData(data)
    const positioned = layoutGraph(transformed.nodes, transformed.edges)
    const fNodes: AttackGraphNodeType[] = positioned.map((p) => ({
      id: p.id,
      type: 'attackGraph',
      position: { x: p.x, y: p.y },
      data: p.data,
      draggable: true,
    }))
    const fEdges: Edge[] = transformed.edges.map((e) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      style: edgeStyle(e.rel),
      markerEnd: { type: 'arrowclosed' as const, color: edgeStyle(e.rel).stroke },
    }))
    return { flowNodes: fNodes, flowEdges: fEdges }
  }, [data])

  const onNodeClick = useCallback(
    (_: React.MouseEvent, node: AttackGraphNodeType) => {
      setSelected(nodes.find((n) => n.id === node.id) ?? null)
    },
    [nodes],
  )

  const loading = graphQuery.isLoading
  const error = graphQuery.isError ? (graphQuery.error instanceof Error ? graphQuery.error.message : '加载失败') : ''

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-5.5 py-3">
        {/* 不限场景：任意 task 均可出攻击图（后端按 task→assignment 聚合）。 */}
        <OwnerPicker value={owner} onChange={setOwner} />
        {nodes.length > 0 && (
          <div className="ml-4 flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-muted">
            {/* 节点：形状分维度（⬡目标 ●资产 ◆凭据 ■立足点 ★漏洞），颜色分类。 */}
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-target">⬢</i>目标
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-asset">●</i>资产
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-credential">◆</i>凭据
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-access">■</i>立足点
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-sev-critical">★</i>漏洞
            </span>
            <span className="h-3 w-px bg-border" />
            {/* 边：两条重点关系边（on 归属走中性灰不列图例）。 */}
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-enables">╌</i>攻击链
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-derives">╌</i>推导
            </span>
          </div>
        )}
        {owner && (
          <label className="ml-auto inline-flex items-center gap-1.5 text-xs text-muted">
            <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} />
            实时
          </label>
        )}
      </div>

      <div className="relative min-h-[420px] flex-1">
        {nodes.length > 0 && (
          <ReactFlow
            colorMode={colorMode}
            nodes={flowNodes}
            edges={flowEdges}
            nodeTypes={nodeTypes}
            onNodeClick={onNodeClick}
            onPaneClick={() => setSelected(null)}
            fitView
            minZoom={0.1}
            proOptions={{ hideAttribution: true }}
          >
            <Background />
            <Controls />
            <MiniMap nodeColor={minimapNodeColor} pannable zoomable />
          </ReactFlow>
        )}

        {loading ? (
          <div className="absolute inset-0 z-10 grid place-items-center bg-background">
            <span className="text-sm text-muted">加载中…</span>
          </div>
        ) : error ? (
          <div className="absolute inset-0 z-10 grid place-items-center bg-background">
            <span className="text-sm text-sev-critical">⚠ {error}</span>
          </div>
        ) : !owner ? (
          <div className="absolute inset-0 z-10 grid place-items-center bg-background">
            <span className="text-sm text-muted">请选择一个扫描查看攻击图</span>
          </div>
        ) : nodes.length === 0 ? (
          <div className="absolute inset-0 z-10 grid place-items-center bg-background">
            <span className="text-sm text-muted">该交战暂无坐实的攻击图节点</span>
          </div>
        ) : null}

        {/* 节点详情面板：Radix Dialog（Esc 关闭、焦点管理免费）。modal=false 保持点其他节点切换选中。
            不用 Portal：Content 的 absolute 定位依赖这层 relative 容器（graph 区域）。 */}
        <Dialog.Root open={!!selected} onOpenChange={(open) => !open && setSelected(null)} modal={false}>
          <Dialog.Content
            onOpenAutoFocus={(e) => {
              e.preventDefault()
              ;(e.currentTarget as HTMLElement).focus()
            }}
            className="absolute right-3 top-3 z-20 w-[300px] max-w-[60%] rounded-lg border border-border bg-surface p-3.5 shadow-xl focus:outline-none"
          >
            {selected && <NodeDetail node={selected} verifiedByOutcome={verificationOutcome(data, selected)} />}
          </Dialog.Content>
        </Dialog.Root>
      </div>

      {/* 取证链：Verifier 每次复检记录（可复现交付 + 合规审计证据源）。有记录才显示。 */}
      {verifications.length > 0 && (
        <div className="flex flex-shrink-0 gap-2.5 overflow-x-auto border-t border-border px-4 py-2.5">
          {verifications.map((v) => (
            <div
              key={v.id}
              className="min-w-[220px] max-w-[320px] rounded-md border border-border border-l-[3px] bg-surface px-3 py-2"
              style={{ borderLeftColor: v.outcome === 'confirmed' ? 'var(--graph-enables)' : 'var(--border)' }}
            >
              <div className="mb-1 flex items-center justify-between">
                <span
                  className="text-xs font-bold"
                  style={{ color: v.outcome === 'confirmed' ? 'var(--graph-enables)' : 'var(--muted)' }}
                >
                  {v.outcome === 'confirmed' ? '✓ 坐实' : '✗ 证伪'}
                </span>
                <span className="text-[11px] text-muted">{v.duration_ms} ms</span>
              </div>
              <p className="m-0 break-all text-[13px] leading-relaxed text-text">lead {v.lead_id}</p>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// verificationOutcome 找 selected 节点 verified_by 指向的取证记录结论（详情面板展示）。
function verificationOutcome(
  data: { verifications: Array<{ id: string; outcome: string }> } | null,
  node: AttackGraphNode,
): string {
  if (!data || !node.verified_by) return ''
  return data.verifications.find((v) => v.id === node.verified_by)?.outcome ?? ''
}

// NodeDetail 渲染选中节点的详情：类型徽标 + 多态标识 + 确证程度 + attrs 补充字段。
function NodeDetail({ node, verifiedByOutcome }: { node: AttackGraphNode; verifiedByOutcome: string }) {
  const summary = typeof node.attrs?.summary === 'string' ? node.attrs.summary : ''
  const severity = typeof node.attrs?.severity === 'string' ? node.attrs.severity : ''
  return (
    <>
      <header className="mb-2 flex items-center justify-between">
        <span className="rounded px-2 py-0.5 text-[11px] font-bold text-white" style={{ background: kindColor(node.kind) }}>
          {kindLabel(node.kind)}
        </span>
        <Dialog.Title className="sr-only">{kindLabel(node.kind)}节点详情</Dialog.Title>
        <Dialog.Close aria-label="关闭" className="text-[13px] text-muted hover:text-text">
          <X className="h-3.5 w-3.5" />
        </Dialog.Close>
      </header>
      <p className="mb-2.5 break-all text-[13px] text-text">{summary || node.ref.locator}</p>
      <div className="flex flex-col gap-1 text-xs text-muted">
        <span>
          标识：{node.ref.domain}/{node.ref.ref_kind}
        </span>
        <span className="break-all">寻址：{node.ref.locator}</span>
        {severity && <span>严重度：{severity}</span>}
        <span className={node.confidence === 'confirmed' ? 'text-graph-enables' : 'text-graph-derives'}>
          {node.confidence === 'confirmed' ? '已坐实（confirmed）' : '假定（assumed，虚线）'}
        </span>
        {verifiedByOutcome && (
          <span>取证结论：{verifiedByOutcome === 'confirmed' ? '复现成功' : '复现失败'}</span>
        )}
        {attrEntries(node.attrs).map(([k, v]) => (
          <span key={k} className="break-all">
            {k}：{v}
          </span>
        ))}
      </div>
    </>
  )
}
