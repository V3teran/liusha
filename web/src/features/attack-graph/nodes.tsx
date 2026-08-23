import { memo } from 'react'
import { Handle, Position, type Node, type NodeProps } from '@xyflow/react'
import { severityColor } from '@/lib/severity'
import type { GraphNodeData } from './graphTransform'
import { nodeSize } from './layout'

// 5 类节点配色——用 index.css 的 --graph-* token（随主题切换深浅两套值）。
// finding 用 severityColor（威胁色），不占语义色位。
const KIND_COLOR: Record<string, string> = {
  target: 'var(--graph-target)', // 交战根（紫）
  asset: 'var(--graph-asset)', // 攻击面（翠绿）
  credential: 'var(--graph-credential)', // 凭据（天蓝）
  access: 'var(--graph-access)', // 立足点（琥珀）
}

function fillColor(data: GraphNodeData): string {
  if (data.kind === 'finding') return severityColor[data.severity ?? ''] ?? 'var(--sev-low)'
  return KIND_COLOR[data.kind] ?? 'var(--graph-asset)'
}

const NODE_STROKE = 'var(--border-strong)'
// assumed（未经 Verifier 坐实）节点用虚线描边，与 confirmed 一眼区分可信度。
const ASSUMED_DASH = '3,3'

// 六边形（target：交战根）。
function hexagonPoints(size: number): string {
  const r = size / 2
  const pts: string[] = []
  for (let i = 0; i < 6; i++) {
    const angle = (Math.PI / 3) * i - Math.PI / 2
    pts.push(`${r + r * Math.cos(angle)},${r + r * Math.sin(angle)}`)
  }
  return pts.join(' ')
}

// 菱形（credential：凭据）。
function diamondPoints(size: number): string {
  const r = size / 2
  return `${r},0 ${size},${r} ${r},${size} 0,${r}`
}

// 星形（finding：坐实的漏洞，攻击链终点视觉锚）。
function starPoints(size: number): string {
  const r = size / 2
  const inner = r * 0.5
  const pts: string[] = []
  for (let i = 0; i < 10; i++) {
    const radius = i % 2 === 0 ? r : inner
    const angle = (Math.PI / 5) * i - Math.PI / 2
    pts.push(`${r + radius * Math.cos(angle)},${r + radius * Math.sin(angle)}`)
  }
  return pts.join(' ')
}

export type AttackGraphNodeType = Node<GraphNodeData, 'attackGraph'>

// 形状分「语义维度」：★星=漏洞(finding) / ⬡六边=目标(target,交战根) / ◆菱=凭据(credential) /
// ■方=立足点(access,多阶段核心) / ●圆=资产(asset,攻击面)。assumed 节点虚线描边。
export const AttackGraphNode = memo(function AttackGraphNode({ data }: NodeProps<AttackGraphNodeType>) {
  const size = nodeSize(data)
  const fill = fillColor(data)
  const dash = data.confidence === 'assumed' ? ASSUMED_DASH : undefined
  const strokeWidth = data.kind === 'finding' ? 2 : 1

  let shape
  if (data.kind === 'finding') {
    shape = <polygon points={starPoints(size)} fill={fill} stroke={NODE_STROKE} strokeWidth={strokeWidth} strokeDasharray={dash} />
  } else if (data.kind === 'target') {
    shape = <polygon points={hexagonPoints(size)} fill={fill} stroke={NODE_STROKE} strokeWidth={strokeWidth} strokeDasharray={dash} />
  } else if (data.kind === 'credential') {
    shape = <polygon points={diamondPoints(size)} fill={fill} stroke={NODE_STROKE} strokeWidth={strokeWidth} strokeDasharray={dash} />
  } else if (data.kind === 'access') {
    // 立足点用方形（稳固落脚点的直觉），内缩 1px 让描边完整可见。
    shape = <rect x={1} y={1} width={size - 2} height={size - 2} rx={2} fill={fill} stroke={NODE_STROKE} strokeWidth={strokeWidth} strokeDasharray={dash} />
  } else {
    // asset：圆形（攻击面）。
    shape = <circle cx={size / 2} cy={size / 2} r={size / 2 - 0.5} fill={fill} stroke={NODE_STROKE} strokeWidth={strokeWidth} strokeDasharray={dash} />
  }

  return (
    <>
      <Handle type="target" position={Position.Top} className="!opacity-0" />
      <div className="flex items-center gap-1.5">
        <svg width={size} height={size} className="flex-shrink-0" viewBox={`0 0 ${size} ${size}`}>
          {shape}
        </svg>
        <span
          className="line-clamp-2 max-w-[130px] whitespace-normal rounded px-1 py-px text-[11px] leading-snug"
          style={{ background: 'color-mix(in oklch, var(--surface) 70%, transparent)', color: 'var(--text)' }}
          title={data.label}
        >
          {data.label}
        </span>
      </div>
      <Handle type="source" position={Position.Bottom} className="!opacity-0" />
    </>
  )
})

export const nodeTypes = { attackGraph: AttackGraphNode }
