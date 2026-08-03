import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { ReactFlow, Background, Controls, MiniMap, type Edge } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { OwnerPicker } from '@/components/OwnerPicker'
import { getMessage, getMilestones } from '@/api/client'
import type { AttackGraphNode, Milestone } from '@/api/types'
import { agentAccent } from '@/lib/agentColor'
import { severityColor } from '@/lib/severity'
import {
  anchorFromPlaceholderId,
  isPlaceholderId,
  toGraphData,
  type GraphNodeData,
} from '@/features/attack-graph/graphTransform'
import { layoutGraph } from '@/features/attack-graph/layout'
import { nodeTypes, type AttackGraphNodeType } from '@/features/attack-graph/nodes'
import { useAttackGraphQuery } from '@/features/attack-graph/useAttackGraphQuery'
import { useColorMode } from '@/hooks/useColorMode'

// 5 类语义节点的中文短标签（详情面板徽标 + minimap 语义对齐）。
function kindLabel(k: string): string {
  switch (k) {
    case 'task':
      return '任务'
    case 'hypothesis':
      return '判断'
    case 'probe':
      return '探测'
    case 'signal':
      return '信号'
    default:
      return '漏洞'
  }
}

// 节点徽标底色（详情面板）：与 nodes.tsx 的 fillColor 同一套 --graph-* 语义。
function kindColor(k: string): string {
  switch (k) {
    case 'task':
      return 'var(--graph-task)'
    case 'hypothesis':
      return 'var(--graph-hypothesis)'
    case 'probe':
      return 'var(--graph-probe)'
    case 'signal':
      return 'var(--graph-signal)'
    default:
      return 'var(--sev-critical)'
  }
}

// edge 样式（7 类语义边）：三条"重点边"用醒目色 + 虚线——depends_on 金(漏洞→漏洞攻击链,最重要)、
// confirms 洋红(证据→漏洞)、informs 青(信号→判断,调查回环)；其余骨干边(spawns/pursues/tests/reveals)
// 走中性灰实线(结构骨架,不抢主色)。颜色用 index.css 的 --graph-* token（随主题切换）。
function edgeStyle(type: string): { stroke: string; strokeWidth: number; strokeDasharray?: string } {
  if (type === 'depends_on') return { stroke: 'var(--graph-depends-on)', strokeWidth: 2, strokeDasharray: '4,4' }
  if (type === 'confirms') return { stroke: 'var(--graph-confirms)', strokeWidth: 2, strokeDasharray: '4,4' }
  if (type === 'informs') return { stroke: 'var(--graph-informs)', strokeWidth: 1.5, strokeDasharray: '2,3' }
  return { stroke: 'var(--border)', strokeWidth: 1.5 }
}

// MiniMap 节点配色：与主图节点配色（nodes.tsx 的 fillColor）同一套语义，只是在这里独立算一遍——
// 不需要引入额外抽象。折叠占位节点在 minimap 上不必区分展开态，统一给个中性色。
// MiniMap 的 nodeColor prop 按 React Flow 泛型 Node（data: Record<string, unknown>）声明，
// 拿到的实际运行时对象就是本图的 AttackGraphNodeType，故内部转型为 GraphNodeData——
// 这是给第三方组件 prop 适配的类型断言，不是绕过真实类型检查。
function minimapNodeColor(node: { data: Record<string, unknown> }): string {
  const d = node.data as unknown as GraphNodeData
  if (d.kind === 'finding') return severityColor[d.severity ?? ''] ?? 'var(--sev-low)'
  if (d.kind === 'collapsed') return 'var(--border-strong)'
  if (d.status === 'failed' || d.status === 'refuted') return 'var(--graph-dead)'
  switch (d.kind) {
    case 'task':
      return 'var(--graph-task)'
    case 'hypothesis':
      return 'var(--graph-hypothesis)'
    case 'signal':
      return 'var(--graph-signal)'
    default: // probe
      return 'var(--graph-probe)'
  }
}

// 完整模式（成果优先关闭）不折叠任何节点：dagre 对全量节点同步布局 + React Flow 同步渲染，
// 真实扫描上千步时会在主线程跑一次很重的同步计算，长时间卡死标签页。成果优先模式没有这个
// 风险——它把死路折叠进占位段，可见节点数天然有限。故只在完整模式下、节点数超阈值时拦一道
// 确认，而不是限制成果优先模式（那样会限制用户看真正需要看的成果骨架）。
const FULL_MODE_NODE_WARN_THRESHOLD = 500

// 执行图页：选 owner（active/passive 均可）→ 后端按 task 自解析会话 → 拉执行图 → React Flow 分层 DAG 渲染。
// 5 类语义节点：任务(task)/判断(hypothesis 想)/探测(probe 做)/信号(signal 得)/漏洞(finding)；
// 7 类语义边：spawns/pursues/tests/reveals 骨干灰实线，informs(回环)/confirms(证实)/depends_on(攻击链) 醒目虚线。
// 布局 dagre（自上而下）；点节点弹详情。
//
// 数据层见 useAttackGraphQuery：react-query 管获取/loading/error/竞态，SSE 驱动重拉替代定时轮询。
export function AttackGraphPage() {
  const [owner, setOwner] = useState('')
  const [selected, setSelected] = useState<AttackGraphNode | null>(null)
  const [live, setLive] = useState(true) // 实时开关：关闭则即便扫描进行中也不订阅 SSE
  const [simplified, setSimplified] = useState(true) // 成果优先：默认只显示通向漏洞的主干路径
  const [expandedAnchors, setExpandedAnchors] = useState<Set<string>>(new Set())
  // 完整模式下节点数超阈值时的用户确认（见 FULL_MODE_NODE_WARN_THRESHOLD）。
  // 切图 / 切回成果优先 / 重新关闭完整模式都应清掉旧确认——不能让"确认过一次"跨图生效。
  const [fullModeConfirmed, setFullModeConfirmed] = useState(false)
  const colorMode = useColorMode() // React Flow 内置控件（Controls/MiniMap）跟随深浅主题，暗色下不再显白块

  const graphQuery = useAttackGraphQuery(owner, live)
  const data = graphQuery.data ?? null
  const nodes = data?.nodes ?? []
  const currentConv = data?.conversation_id ?? ''

  const milestonesMutation = useMutation({
    mutationFn: () => getMilestones(owner, currentConv),
  })
  const milestonesResetRef = useRef(milestonesMutation.reset)
  milestonesResetRef.current = milestonesMutation.reset

  // 切 owner / 图变化（task_id 变了）时清掉选中态与本地折叠展开态（旧图的锚点在新图里无意义）。
  // 副作用（重置 mutation 状态）必须在 useEffect 里做，不能在渲染阶段直接调——渲染函数体应
  // 保持纯粹，StrictMode 下渲染阶段的副作用会被双调用，容易产生难查的重复触发。
  const taskID = data?.task_id ?? ''
  useEffect(() => {
    if (!taskID) return
    setSelected(null)
    setExpandedAnchors(new Set())
    setFullModeConfirmed(false)
    milestonesResetRef.current()
  }, [taskID])

  // 切回成果优先或重新打开完整模式都要求重新确认——不能让上一次的确认跨切换生效
  // （用户很可能在关闭完整模式后忘了这件事，下次打开不该直接跳过警告）。
  useEffect(() => {
    setFullModeConfirmed(false)
  }, [simplified])

  const fullModeNeedsConfirm = !simplified && nodes.length > FULL_MODE_NODE_WARN_THRESHOLD && !fullModeConfirmed

  // 点开节点看原文：按需拉 selected.ref 指向的那一条消息，不预拉整段会话正文。
  // 旧实现在拿到会话 id 时就循环翻页拉全部消息塞进 state——长扫描（上千条事件）时
  // 把整段会话正文全灌进内存，且这次全量拉取本身就是一次不小的网络开销；而实际只有
  // 用户点开的那几个节点需要看原文。enabled 门控：没选中节点 / 节点没有 ref（无原文可看）/
  // 没有会话时都不发请求。
  const contentQuery = useQuery({
    queryKey: ['attack-graph', 'message', currentConv, selected?.ref],
    queryFn: () => getMessage(currentConv, selected!.ref!),
    enabled: !!currentConv && !!selected?.ref,
  })

  const loadMilestones = () => {
    if (!owner || !currentConv) return
    milestonesMutation.mutate()
  }

  // 图数据转换 + dagre 布局 → React Flow nodes/edges。
  // fullModeNeedsConfirm 时不跑这次计算：toGraphData(collapse=false) 全量保留 + dagre 对
  // 上千节点同步布局是真正昂贵的一步，门控必须在这里生效，不能只在 UI 上藏起来不渲染
  // （否则用户没确认，计算已经在主线程跑完，防护形同虚设）。
  const { flowNodes, flowEdges } = useMemo(() => {
    if (fullModeNeedsConfirm) return { flowNodes: [], flowEdges: [] }
    const transformed = toGraphData(data, simplified, expandedAnchors)
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
      style: edgeStyle(e.type),
      markerEnd: { type: 'arrowclosed' as const, color: edgeStyle(e.type).stroke },
    }))
    return { flowNodes: fNodes, flowEdges: fEdges }
  }, [data, simplified, expandedAnchors, fullModeNeedsConfirm])

  const onNodeClick = useCallback(
    (_: React.MouseEvent, node: AttackGraphNodeType) => {
      // 占位节点：toggle 该折叠段（展开 ⇄ 收起，渐进披露），不钻取原文。
      if (isPlaceholderId(node.id)) {
        const anchor = anchorFromPlaceholderId(node.id)
        setExpandedAnchors((prev) => {
          const s = new Set(prev)
          if (s.has(anchor)) s.delete(anchor)
          else s.add(anchor)
          return s
        })
        return
      }
      setSelected(nodes.find((n) => n.id === node.id) ?? null)
    },
    [nodes],
  )

  const fullContent = contentQuery.data?.Content ?? ''
  const fullContentLoading = contentQuery.isLoading
  const loading = graphQuery.isLoading
  const error = graphQuery.isError ? (graphQuery.error instanceof Error ? graphQuery.error.message : '加载失败') : ''
  const renderPending = !!data?.nodes?.length && graphQuery.isFetching

  const milestones: Milestone[] = milestonesMutation.data ?? []
  const milestonesLoading = milestonesMutation.isPending
  const milestonesErr = milestonesMutation.isError
    ? milestonesMutation.error instanceof Error
      ? milestonesMutation.error.message
      : '生成失败'
    : ''

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-5.5 py-3">
        {/* 不限场景：任意 task 均可出执行图。 */}
        <OwnerPicker value={owner} onChange={setOwner} />
        {nodes.length > 0 && (
          <div className="ml-4 flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-muted">
            {/* 节点：形状分维度（⬡任务 ●判断/探测 ◆信号 ★漏洞），颜色分子类。 */}
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-task">⬢</i>任务
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-hypothesis">●</i>判断
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-probe">●</i>探测
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-signal">◆</i>信号
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-dead">●</i>死路
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-sev-critical">★</i>漏洞
            </span>
            <span className="h-3 w-px bg-border" />
            {/* 边：三条重点语义边（骨干边走中性灰不列图例）。 */}
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-confirms">╌</i>证实
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-informs">╌</i>回环
            </span>
            <span className="inline-flex items-center gap-1.5">
              <i className="text-xs not-italic text-graph-depends-on">╌</i>攻击链
            </span>
          </div>
        )}
        {nodes.length > 0 && (
          <button
            type="button"
            disabled={milestonesLoading}
            onClick={loadMilestones}
            className="ml-auto rounded-md border border-border px-3 py-1 text-xs text-text hover:border-accent disabled:opacity-50"
          >
            {milestonesLoading ? '生成中…' : '里程碑摘要'}
          </button>
        )}
        {owner && (
          <label className="inline-flex items-center gap-1.5 text-xs text-muted">
            <input type="checkbox" checked={simplified} onChange={(e) => setSimplified(e.target.checked)} />
            成果优先
          </label>
        )}
        {owner && (
          <label className="inline-flex items-center gap-1.5 text-xs text-muted">
            <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} />
            实时
          </label>
        )}
      </div>

      {(milestones.length > 0 || milestonesErr) && (
        <div className="flex flex-shrink-0 gap-2.5 overflow-x-auto border-b border-border px-4 py-2.5">
          {milestonesErr && <span className="text-sm text-sev-critical">⚠ {milestonesErr}</span>}
          {milestones.map((m) => (
            <div
              key={m.agent}
              className="min-w-[240px] max-w-[340px] rounded-md border border-border border-l-[3px] bg-surface px-3 py-2"
              style={{ borderLeftColor: agentAccent(m.agent).accent }}
            >
              <div className="mb-1 flex items-center justify-between">
                <span className="text-xs font-bold" style={{ color: agentAccent(m.agent).accent }}>
                  {m.agent}
                </span>
                <span className="text-[11px] text-muted">{m.node_count} 步</span>
              </div>
              <p className="m-0 text-[13px] leading-relaxed text-text">{m.summary}</p>
            </div>
          ))}
        </div>
      )}

      <div className="relative min-h-[420px] flex-1">
        {nodes.length > 0 && !fullModeNeedsConfirm && (
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
            {/* 大图（上千节点）没有小地图无法导航——缩放到能看清单个节点的程度后，
                找不到自己在图的哪个区域。pannable+zoomable 让点/拖小地图直接跳转视口。 */}
            <MiniMap nodeColor={minimapNodeColor} pannable zoomable />
          </ReactFlow>
        )}

        {loading || renderPending ? (
          <div className="absolute inset-0 z-10 grid place-items-center bg-background">
            <span className="text-sm text-muted">加载中…</span>
          </div>
        ) : error ? (
          <div className="absolute inset-0 z-10 grid place-items-center bg-background">
            <span className="text-sm text-sev-critical">⚠ {error}</span>
          </div>
        ) : !owner ? (
          <div className="absolute inset-0 z-10 grid place-items-center bg-background">
            <span className="text-sm text-muted">请选择一个扫描查看执行图</span>
          </div>
        ) : nodes.length === 0 ? (
          <div className="absolute inset-0 z-10 grid place-items-center bg-background">
            <span className="text-sm text-muted">该扫描暂无执行图数据</span>
          </div>
        ) : fullModeNeedsConfirm ? (
          <div className="absolute inset-0 z-10 grid place-items-center bg-background">
            <div className="flex max-w-sm flex-col items-center gap-3 text-center">
              <span className="text-sm text-text">
                完整模式将渲染全部 {nodes.length} 个节点，节点数较多时浏览器可能长时间无响应。
              </span>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => setFullModeConfirmed(true)}
                  className="rounded-md border border-border px-3 py-1.5 text-xs text-text hover:border-accent"
                >
                  仍然渲染全部节点
                </button>
                <button
                  type="button"
                  onClick={() => setSimplified(true)}
                  className="rounded-md border border-accent bg-accent/10 px-3 py-1.5 text-xs text-accent"
                >
                  返回成果优先
                </button>
              </div>
            </div>
          </div>
        ) : null}

        {/* 节点详情面板：Radix Dialog 替代原生 <aside>，换来免费的键盘可达性——
            Esc 关闭、打开时焦点自动移入面板、关闭后焦点还原到触发元素（原生 div 全都没有，
            键盘用户点不开也关不掉）。modal=false：保持原有交互——面板打开时仍可点其他节点
            切换选中，或点画布空白关闭，不像 FindingDrawer 那种需要遮罩挡住背景的模态抽屉。
            不用 Dialog.Portal：Content 的 absolute 定位依赖这层 relative 容器（graph 区域），
            Portal 默认挂到 document.body 会让定位相对视口而不是这个容器，故直接原地渲染。 */}
        <Dialog.Root open={!!selected} onOpenChange={(open) => !open && setSelected(null)} modal={false}>
          <Dialog.Content
            onOpenAutoFocus={(e) => {
              // 默认焦点会落到面板第一个可聚焦元素（关闭按钮）；这里改为聚焦面板容器本身，
              // 避免打开瞬间关闭按钮就带 focus 环，视觉上比较突兀，同时仍保证 Tab/Esc 可用。
              e.preventDefault()
              ;(e.currentTarget as HTMLElement).focus()
            }}
            className="absolute right-3 top-3 z-20 w-[280px] max-w-[60%] rounded-lg border border-border bg-surface p-3.5 shadow-xl focus:outline-none"
          >
            {selected && (
              <>
                <header className="mb-2 flex items-center justify-between">
                  <span
                    className="rounded px-2 py-0.5 text-[11px] font-bold text-white"
                    style={{ background: kindColor(selected.kind) }}
                  >
                    {kindLabel(selected.kind)}
                  </span>
                  <Dialog.Title className="sr-only">{kindLabel(selected.kind)}节点详情</Dialog.Title>
                  <Dialog.Close aria-label="关闭" className="text-[13px] text-muted hover:text-text">
                    <X className="h-3.5 w-3.5" />
                  </Dialog.Close>
                </header>
                <p className="mb-2.5 break-all text-[13px] text-text">{selected.title}</p>
                {fullContentLoading ? (
                  <p className="mb-2.5 text-xs text-muted">原文加载中…</p>
                ) : (
                  fullContent && (
                    <pre className="mb-2.5 max-h-[260px] overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-background px-2.5 py-2 text-xs leading-relaxed text-text">
                      {fullContent}
                    </pre>
                  )
                )}
                <div className="flex flex-col gap-1 text-xs text-muted">
                  {selected.agent && <span>子代理：{selected.agent}</span>}
                  {selected.host && <span>站点：{selected.host}</span>}
                  {selected.severity && <span>严重度：{selected.severity}</span>}
                  {typeof selected.duration_ms === 'number' && selected.duration_ms > 0 && (
                    <span>耗时：{selected.duration_ms} ms</span>
                  )}
                  {typeof selected.tokens === 'number' && selected.tokens > 0 && <span>Token：{selected.tokens}</span>}
                  {selected.provenance === 'llm' && <span className="text-graph-hypothesis">LLM 事后提炼（虚线）</span>}
                  {(selected.status === 'failed' || selected.status === 'refuted') && (
                    <span className="text-sev-critical">死路 / {selected.status === 'refuted' ? '判断被否定' : '探测失败'}</span>
                  )}
                </div>
              </>
            )}
          </Dialog.Content>
        </Dialog.Root>
      </div>
    </div>
  )
}
