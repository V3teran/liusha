import { memo } from 'react'
import { Handle, Position, type Node, type NodeProps } from '@xyflow/react'
import { ChevronDown, ChevronRight, Compass, Search } from 'lucide-react'
import { severityColor } from '@/lib/severity'
import type { GraphNodeData } from './graphTransform'
import { nodeSize } from './layout'

// 5 类语义节点的配色——用 index.css 里的 --graph-* token（随主题切换深浅两套值），
// 不在组件里硬编码色值。漏洞用 severityColor（威胁色）。
const TASK_COLOR = 'var(--graph-task)' // 任务：结构边界（紫）
const HYPOTHESIS_COLOR = 'var(--graph-hypothesis)' // 判断·想（天蓝）
const PROBE_COLOR = 'var(--graph-probe)' // 探测·做（翠绿）
const SIGNAL_COLOR = 'var(--graph-signal)' // 信号·得（青）
const DEAD_FILL = 'var(--graph-dead)' // 死路（探测失败 / 判断被否定）

// 探测失败 / 判断被否定的死路状态。
function isDeadStatus(data: GraphNodeData): boolean {
  return data.status === 'failed' || data.status === 'refuted'
}

function fillColor(data: GraphNodeData): string {
  if (data.kind === 'finding') return severityColor[data.severity ?? ''] ?? 'var(--sev-low)'
  if (isDeadStatus(data)) return DEAD_FILL
  switch (data.kind) {
    case 'task':
      return TASK_COLOR
    case 'hypothesis':
      return HYPOTHESIS_COLOR
    case 'signal':
      return SIGNAL_COLOR
    default: // probe
      return PROBE_COLOR
  }
}

// 节点默认描边（弱化，不抢主色）与死路描边——中性色，跟随主题切换。
const NODE_STROKE = 'var(--border-strong)'
const DEAD_STROKE = 'var(--faint)'
// 组合漏洞（chained）用金色强化描边 + 光晕，与 depends_on 边同一语义色，呼应"这是攻击链的一环"。
const CHAINED_ACCENT = 'var(--graph-depends-on)'
// LLM 事后提炼的节点（provenance=llm）用虚线描边——标注"这是 LLM 猜的，可信度低于确定性派生
// (derived) 与 agent 自标 (agent)"，让观察者一眼分清语义节点来源可信度。
const LLM_DASH = '3,3'

// 六边形路径（供 task 节点用，svg polygon points 由 size 现算）。
function hexagonPoints(size: number): string {
  const r = size / 2
  const pts: string[] = []
  for (let i = 0; i < 6; i++) {
    const angle = (Math.PI / 3) * i - Math.PI / 2
    pts.push(`${r + r * Math.cos(angle)},${r + r * Math.sin(angle)}`)
  }
  return pts.join(' ')
}

// 菱形路径（供 signal 节点用：四顶点，上右下左）。
function diamondPoints(size: number): string {
  const r = size / 2
  return `${r},0 ${size},${r} ${r},${size} 0,${r}`
}

// 星形路径（供 finding 节点用）。
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

// 形状表「语义维度」：★星=漏洞(finding) / ⬡六边=任务(task,结构边界) / ◆菱=信号(signal,关键观察) /
// ●圆=判断(hypothesis 想) 与探测(probe 做)（二者靠颜色区分：天蓝=想，翠绿=做）。
export const AttackGraphNode = memo(function AttackGraphNode({ data }: NodeProps<AttackGraphNodeType>) {
  const size = nodeSize(data)

  // 折叠占位节点：点击 toggle 展开/收起该探索段（点击事件在父组件用 onNodeClick 处理，此处只画外观）。
  // 交互态（展开⇄收起）与段类型（开场/探索）用结构化图标表达，不在数据层拼 emoji/箭头字符——
  // 图标组件天然可测（按 role/name 查询）、可换、可加 hover 态，字符串符号做不到这些。
  if (data.kind === 'collapsed') {
    // 展开态用 chevron-down（"点击收起"），折叠态用 chevron-right（"点击展开"）。段类型图标
    // 画在 chevron 之前（侦察用指北针 Compass，探索用放大镜 Search）。
    const ToggleIcon = data.expanded ? ChevronDown : ChevronRight
    const SegmentIcon = data.opening ? Compass : Search
    return (
      <>
        <Handle type="target" position={Position.Top} className="!opacity-0" />
        <div
          className="flex items-center gap-1 rounded-full border px-2 py-1"
          style={{
            borderColor: TASK_COLOR,
            borderStyle: data.expanded ? 'solid' : 'dashed',
            background: data.expanded ? 'color-mix(in oklch, var(--graph-task) 18%, transparent)' : 'transparent',
          }}
        >
          <SegmentIcon size={12} color={TASK_COLOR} strokeWidth={2} />
          <span className="whitespace-nowrap text-[11px]" style={{ color: 'var(--text)' }}>
            {data.label}
          </span>
          <ToggleIcon size={12} color={TASK_COLOR} strokeWidth={2} />
        </div>
        <Handle type="source" position={Position.Bottom} className="!opacity-0" />
      </>
    )
  }

  const fill = fillColor(data)
  const dead = isDeadStatus(data)
  const isChained = data.kind === 'finding' && data.chained
  const isLLM = data.provenance === 'llm'

  const stroke = isChained ? CHAINED_ACCENT : dead ? DEAD_STROKE : NODE_STROKE
  const strokeWidth = isChained ? 3 : 1
  const dash = isLLM ? LLM_DASH : undefined

  let shape
  if (data.kind === 'finding') {
    shape = <polygon points={starPoints(size)} fill={fill} stroke={stroke} strokeWidth={strokeWidth} strokeDasharray={dash} />
  } else if (data.kind === 'task') {
    shape = <polygon points={hexagonPoints(size)} fill={fill} stroke={stroke} strokeWidth={strokeWidth} strokeDasharray={dash} />
  } else if (data.kind === 'signal') {
    shape = <polygon points={diamondPoints(size)} fill={fill} stroke={stroke} strokeWidth={strokeWidth} strokeDasharray={dash} />
  } else {
    // hypothesis / probe：圆形（颜色区分想/做）。
    shape = (
      <circle
        cx={size / 2}
        cy={size / 2}
        r={size / 2 - (dead ? 1 : 0.5)}
        fill={fill}
        stroke={stroke}
        strokeWidth={strokeWidth}
        strokeDasharray={dash}
      />
    )
  }

  return (
    <>
      <Handle type="target" position={Position.Top} className="!opacity-0" />
      <div
        className="flex items-center gap-1.5"
        style={{
          opacity: data.dim ? 0.28 : 1,
          filter: isChained ? `drop-shadow(0 0 6px color-mix(in oklch, ${CHAINED_ACCENT} 60%, transparent))` : undefined,
        }}
      >
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
