/**
 * 探索图画布：React Flow 只读封装。
 * 自定义节点（nodeTypes 模块级常量，避免每渲染重建）、聚焦高亮由上层
 * applyFocus 算好传入；节点数变化或会话切换时 fitView 复位视口。
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
  type Edge,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import './exploration.css'
import { ExplorationNodeCard } from './nodes/ExplorationNodeCard'
import { kindProfile } from './kindTheme'
import { EDGE_COLORS, type ExplorationFlowNode } from './layout'

const nodeTypes = { exploration: ExplorationNodeCard }

// useReactFlow 在 <ReactFlow> 之外取实例，必须显式包一层 Provider
/** 静态全图适配的缩放下限：节点卡片正文在此缩放下仍可辨认。 */
const LEGIBLE_MIN_ZOOM = 0.6

export function GraphCanvas(props: {
  nodes: ExplorationFlowNode[]
  edges: Edge[]
  /** 会话切换（或其它需要视口复位的事件键） */
  resetKey: string
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
  onNodeClick,
}: {
  nodes: ExplorationFlowNode[]
  edges: Edge[]
  resetKey: string
  onNodeClick: (id: string) => void
}) {
  const { fitView } = useReactFlow()
  const prevCount = useRef(-1)
  const prevReset = useRef(resetKey)
  // 最新节点经 ref 读取：fitView 回调触发时取当下状态（含 running 前沿）
  const nodesRef = useRef(nodes)
  nodesRef.current = nodes
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
        // 智能初始视口：图在跑（存在 running 节点）时定位到活动前沿，随新节点
        // 到来持续跟进；图静止时适配全图，但缩放不低于可读下限——大图全量装进视口时
        // 节点字号会缩到不可辨认，全局鸟瞰交给右下角小地图，主画布保证卡片可读。
        const running = nodesRef.current.filter((n) => n.data.node.state === 'running').map((n) => n.id)
        if (running.length > 0) {
          void fitView({ nodes: running.map((id) => ({ id })), padding: 0.35, duration: 300, minZoom: 0.3, maxZoom: 1 })
        } else {
          void fitView({ padding: 0.15, duration: 300, minZoom: LEGIBLE_MIN_ZOOM, maxZoom: 1 })
        }
      })
    }
  }, [nodes.length, resetKey, fitView])

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
      {/* 图例放左上：Controls 默认在左下，避免遮挡。
          四个样本统一为"线"：三种关系线型 + 执行中的流动动画态 */}
      <Panel position="top-left">
        <div className="flex items-center gap-3 rounded-lg border border-border bg-surface/90 px-3 py-1.5 text-[10px] text-text-secondary backdrop-blur-sm">
          <span className="flex items-center gap-1.5">
            <svg width="18" height="4" aria-hidden>
              <line x1="0" y1="2" x2="18" y2="2" stroke={EDGE_COLORS.generates} strokeWidth="2" />
            </svg>
            推导
          </span>
          <span className="flex items-center gap-1.5">
            <svg width="18" height="4" aria-hidden>
              <line x1="0" y1="2" x2="18" y2="2" stroke={EDGE_COLORS.triggers} strokeWidth="2" strokeDasharray="6 4" />
            </svg>
            滋生
          </span>
          <span className="flex items-center gap-1.5">
            <svg width="18" height="4" aria-hidden>
              <line x1="0" y1="2" x2="18" y2="2" stroke={EDGE_COLORS.supersedes} strokeWidth="2" strokeDasharray="10 6" />
            </svg>
            吸收
          </span>
          <span className="flex items-center gap-1.5">
            <svg width="18" height="4" aria-hidden>
              <line x1="0" y1="2" x2="18" y2="2" stroke={EDGE_COLORS.depends} strokeWidth="2" strokeDasharray="2 4" />
            </svg>
            依赖
          </span>
          {/* 关系线型（左）与活动状态（右）是两类语义，分组线隔开 */}
          <span className="mx-0.5 h-3 w-px bg-border" aria-hidden />
          <span className="flex items-center gap-1.5">
            <svg width="18" height="4" aria-hidden>
              <line
                className="exp-legend-flow"
                x1="0"
                y1="2"
                x2="18"
                y2="2"
                stroke={EDGE_COLORS.generates}
                strokeWidth="2"
                strokeDasharray="4 3"
              />
            </svg>
            执行中
          </span>
        </div>
      </Panel>
    </ReactFlow>
  )
}
