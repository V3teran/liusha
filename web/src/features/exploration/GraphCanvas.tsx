/**
 * 探索图画布：React Flow 只读封装。
 * 自定义节点（nodeTypes 模块级常量，避免每渲染重建）、聚焦高亮由上层
 * applyFocus 算好传入；节点数变化或任务切换时 fitView 复位视口；选着节点时
 * 改由来路适配管视口——它的来路掉出视野才把整条来路收进来。
 */

import { useEffect, useRef } from 'react'
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  Panel,
  useReactFlow,
  useStore,
  type Edge,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import './exploration.css'
import { ExplorationNodeCard } from './nodes/ExplorationNodeCard'
import { kindProfile } from './kindTheme'
import { EDGE_LOOKS, EDGE_LOOK_LABELS, EDGE_LOOK_ORDER, NODE_HEIGHT, type ExplorationFlowNode } from './layout'
import { LeadMarkFollowed, LeadMark } from './LeadMark'
import { CHAIN_MARGIN, anyOutside, detailReserve, frameNodes } from './viewport'

const nodeTypes = { exploration: ExplorationNodeCard }

// useReactFlow 在 <ReactFlow> 之外取实例，必须显式包一层 Provider
/** 静态全图适配的缩放下限：节点卡片正文在此缩放下仍可辨认。 */
const LEGIBLE_MIN_ZOOM = 0.6
/** 全图适配的留白（占视口比例，与 fitView 的 padding 同义） */
const FIT_PADDING = 0.15
/** 大图从根目标读起时的缩放与左侧留白（px） */
const ANCHOR_ZOOM = 0.9
const ANCHOR_MARGIN = 48

/** 根目标：代表任务本身的目标（来源 user，最早创建）。布局自左向右，它在最左列。 */
function rootObjective(nodes: ExplorationFlowNode[]): ExplorationFlowNode | undefined {
  return nodes
    .filter((n) => n.data.node.kind === 'objective' && n.data.node.source_type === 'user')
    .sort((a, b) => a.data.node.created_at.localeCompare(b.data.node.created_at))[0]
}

export function GraphCanvas(props: {
  nodes: ExplorationFlowNode[]
  edges: Edge[]
  /** 任务切换（或其它需要视口复位的事件键） */
  resetKey: string
  /** 视口复位时对准的节点（打开的节点，如从发现跳来的结果节点）；空 = 按图的活动状态定位 */
  focusNodeId: string
  onNodeClick: (id: string) => void
}) {
  return (
    <ReactFlowProvider>
      <GraphCanvasInner {...props} />
    </ReactFlowProvider>
  )
}

function GraphCanvasInner({
  nodes,
  edges,
  resetKey,
  focusNodeId,
  onNodeClick,
}: {
  nodes: ExplorationFlowNode[]
  edges: Edge[]
  resetKey: string
  focusNodeId: string
  onNodeClick: (id: string) => void
}) {
  const { fitView, getNodesBounds, getViewport, setViewport } = useReactFlow()
  const viewportWidth = useStore((st) => st.width)
  const viewportHeight = useStore((st) => st.height)
  const sizeRef = useRef({ w: viewportWidth, h: viewportHeight })
  sizeRef.current = { w: viewportWidth, h: viewportHeight }
  const prevCount = useRef(-1)
  const prevReset = useRef(resetKey)
  // 最新节点经 ref 读取：fitView 回调触发时取当下状态（含 running 前沿）
  const nodesRef = useRef(nodes)
  nodesRef.current = nodes
  // 对准的节点同样经 ref 读取：复位时若有选中的节点，视口交给下面的来路适配
  const focusRef = useRef(focusNodeId)
  focusRef.current = focusNodeId
  // 已排程守卫：React Flow 初始化会重建 viewport helpers（fitView 身份变化）导致
  // effect 重跑，若靠 cleanup 取消 rAF 会把还没执行的 fitView 撤掉，且数量守卫
  // 不再补发——视口停在默认缩放的竞态。排程后由守卫挡住重复调度，不做取消。
  const fitPendingRef = useRef(false)

  useEffect(() => {
    const countChanged = nodes.length !== prevCount.current
    const resetChanged = resetKey !== prevReset.current
    prevCount.current = nodes.length
    prevReset.current = resetKey
    // 轮询刷新但节点数没变时不复位视口——用户正在看的区域不被打断
    if (nodes.length > 0 && (countChanged || resetChanged) && !fitPendingRef.current) {
      fitPendingRef.current = true
      requestAnimationFrame(() => {
        fitPendingRef.current = false
        // 智能初始视口：图在跑（存在 running 节点）时定位到活动前沿，随新节点到来持续跟进；
        // 图静止且全图在可读缩放下装得下时适配全图。装不下的大图从根目标读起（布局自左向右，
        // 根在最左列）：以可读缩放把它放在左侧、垂直居中——按下限缩放去「适配」只会把视口
        // 落在整张图的几何中心，那里通常是几列之间的空白。全局鸟瞰交给右下角小地图。
        // 选着节点时这里不动：视口属于来路适配（下一个 effect）。
        const focus = focusRef.current
        if (focus && nodesRef.current.some((n) => n.id === focus)) return
        const running = nodesRef.current.filter((n) => n.data.node.state === 'running').map((n) => n.id)
        if (running.length > 0) {
          void fitView({ nodes: running.map((id) => ({ id })), padding: 0.35, duration: 300, minZoom: 0.3, maxZoom: 1 })
        } else {
          const { w, h } = sizeRef.current
          const bounds = getNodesBounds(nodesRef.current)
          const fitZoom = Math.min(w / (bounds.width * (1 + 2 * FIT_PADDING)), h / (bounds.height * (1 + 2 * FIT_PADDING)))
          const root = rootObjective(nodesRef.current)
          if (fitZoom >= LEGIBLE_MIN_ZOOM || !root || w === 0) {
            void fitView({ padding: FIT_PADDING, duration: 300, minZoom: LEGIBLE_MIN_ZOOM, maxZoom: 1 })
          } else {
            void setViewport(
              {
                x: ANCHOR_MARGIN - root.position.x * ANCHOR_ZOOM,
                y: h / 2 - (root.position.y + NODE_HEIGHT / 2) * ANCHOR_ZOOM,
                zoom: ANCHOR_ZOOM,
              },
              { duration: 300 },
            )
          }
        }
      })
    }
  }, [nodes.length, resetKey, fitView, getNodesBounds, setViewport])

  // 来路适配：选中的节点变了、或选着节点时图长了（布局会挪动节点），看它的来路上有没有节点
  // 掉出视野——有才把整条来路收进来；都在视野里就不动，用户点的正是他在看的地方。详情浮层
  // 盖在画布右缘，不算视野：收进来的来路不能藏在它底下。取消选中不恢复视口。
  // 是否移动只看「有没有出界」，所以这个 effect 多跑一遍（StrictMode、视口工具重建）无妨。
  const chainKey = focusNodeId ? `${focusNodeId}#${nodes.length}` : ''
  useEffect(() => {
    if (!chainKey) return
    // 画布刚挂上时还没量出尺寸（ResizeObserver 晚一两帧）：等量出来再算，至多等半秒。
    let frames = 0
    let cancelled = false
    const tick = () => {
      if (cancelled) return
      const { w, h } = sizeRef.current
      if (w === 0) {
        if (++frames < 30) requestAnimationFrame(tick)
        return
      }
      const chain = nodesRef.current.filter((n) => n.className !== 'exp-dimmed')
      if (chain.length === 0) return
      const reserve = detailReserve(w)
      if (!anyOutside(chain, getViewport(), w, h, reserve)) return
      const next = frameNodes(chain, w - reserve - 2 * CHAIN_MARGIN, h - 2 * CHAIN_MARGIN)
      if (next) void setViewport(next, { duration: 300 })
    }
    requestAnimationFrame(tick)
    return () => {
      cancelled = true
    }
  }, [chainKey, getViewport, setViewport])

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      onNodeClick={(_, node) => onNodeClick(node.id)}
      minZoom={0.05}
      maxZoom={2}
      nodesDraggable={false}
      nodesConnectable={false}
      edgesFocusable={false}
      elementsSelectable
    >
      <Background gap={24} size={1.4} />
      <Controls showInteractive={false} />
      {/* 窄屏（< 768）不显示小地图：固定 200px 方块会盖住画布近半，手机上用双指缩放鸟瞰。 */}
      <MiniMap
        className="hidden md:block"
        nodeColor={(n) => kindProfile((n.data as { node?: { kind?: string } })?.node?.kind ?? '').accent}
        pannable
        zoomable
      />
      {/* 图例放左上：Controls 默认在左下，避免遮挡。固定列出全部关系与标记：图例是这张图的
          词汇表，看图的人拿它对照线型，不随图上此刻有什么而增减。样本取自画法本身（EDGE_LOOKS）；
          关系线型、活动状态、线索标记是三类语义，分组线隔开。窄屏放不下一行时折行，宽度不超过画布 */}
      <Panel position="top-left">
        <div className="flex max-w-[calc(100vw-3rem)] flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border border-border bg-surface/90 px-3 py-1.5 text-2xs text-text-secondary backdrop-blur-sm">
          {EDGE_LOOK_ORDER.map((look) => (
            <span key={look} className="flex items-center gap-1.5">
              <svg width="28" height="4" aria-hidden>
                <line
                  x1="0"
                  y1="2"
                  x2="28"
                  y2="2"
                  stroke={EDGE_LOOKS[look].color}
                  strokeWidth={EDGE_LOOKS[look].width}
                  strokeDasharray={EDGE_LOOKS[look].dash}
                />
              </svg>
              {EDGE_LOOK_LABELS[look]}
            </span>
          ))}
          <span className="mx-0.5 h-3 w-px bg-border" aria-hidden />
          <span className="flex items-center gap-1.5">
            <svg width="28" height="4" aria-hidden>
              <line
                className="exp-legend-flow"
                x1="0"
                y1="2"
                x2="28"
                y2="2"
                stroke={EDGE_LOOKS.generates.color}
                strokeWidth={EDGE_LOOKS.generates.width}
                strokeDasharray="4 3"
              />
            </svg>
            执行中
          </span>
          <span className="mx-0.5 h-3 w-px bg-border" aria-hidden />
          {/* 图例里的线索样例就是立在卡片右上角的那串脚印：同一个组件 */}
          <span className="flex items-center gap-1.5">
            <LeadMark aria-hidden title="执行者已踩通、尚未推进到结论的路径" />
            <LeadMarkFollowed aria-hidden title="被动作认领推进过、随后关闭的线索" />
            线索
          </span>
        </div>
      </Panel>
    </ReactFlow>
  )
}
