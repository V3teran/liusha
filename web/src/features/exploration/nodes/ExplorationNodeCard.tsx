/**
 * 探索图节点卡片（唯一自定义节点，React Flow nodeTypes 注册名 'exploration'）。
 *
 * 固定 264×104（与 dagre 布局假设严格一致），全部缩放级别渲染同一结构。
 * kind 编码三层：全卡淡染底（远距离的面积色，打开即全图时的主识别通道）
 * → accent 图标（中距离）→ 中文标签（近处的精确语义）。
 * 文字一律中性 token：小号彩字在任何主题都难保 4.5:1。
 */

import { memo } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { severityLabel } from '@/lib/severity'
import { cn } from '@/lib/utils'
import { kindProfile, nodeChips, nodeTitle, SEVERITY_DOTS } from '../kindTheme'
import type { ExplorationFlowNode } from '../layout'

export const ExplorationNodeCard = memo(({ data }: NodeProps<ExplorationFlowNode>) => {
  const { node } = data
  const profile = kindProfile(node.kind)
  const Icon = profile.icon
  const title = nodeTitle(node) || '（无内容）'
  const chips = nodeChips(node)

  return (
    <div
      className={cn(
        'exp-card group relative flex h-26 w-[264px] flex-col overflow-hidden rounded-xl border border-border',
        'pl-3 shadow-xs transition-[border-color,box-shadow] duration-150',
        'hover:border-border-strong hover:shadow-md',
        node.state === 'running' && 'exp-card-running',
      )}
      style={{ background: `color-mix(in srgb, ${profile.accent} 10%, var(--surface))` }}
    >
      <div className="flex items-center gap-1.5 pr-3 pt-2">
        <Icon className="h-3.5 w-3.5 flex-shrink-0" style={{ color: profile.accent }} />
        <span className="text-[10px] font-semibold tracking-wider text-text-secondary">{profile.label}</span>
        {chips.priority && (
          <span
            className={cn('ml-auto rounded bg-surface-2 px-1 py-px font-mono text-[10px] font-semibold', chips.priority.className)}
            title="规划者评定的优先级（P0 关键 / P1 高 / P2 中 / P3 低）"
          >
            {chips.priority.code}
          </span>
        )}
      </div>

      <p className="mt-1 line-clamp-2 pr-3 text-xs leading-[18px] text-text" title={title}>
        {title}
      </p>

      {/* 徽章行统一语法：可选色点 + 中性文字——色相只落在点上，文字永可读 */}
      <div className="mt-auto flex items-center gap-1.5 overflow-hidden pb-2 pr-3 text-[10px] text-text-secondary">
        {chips.state && (
          <span className="inline-flex flex-shrink-0 items-center gap-1">
            <span className={cn('h-1.5 w-1.5 rounded-full', chips.state.dotClass)} />
            {chips.state.label}
          </span>
        )}
        {chips.category && <span className="flex-shrink-0">{chips.category}</span>}
        {chips.severity && (
          <span className="inline-flex flex-shrink-0 items-center gap-1">
            <span
              className="h-1.5 w-1.5 rounded-full"
              style={{ background: SEVERITY_DOTS[chips.severity] ?? '#64748b' }}
            />
            {severityLabel[chips.severity] ?? chips.severity}
          </span>
        )}
        {chips.confidence && <span className="flex-shrink-0">{chips.confidence}</span>}
      </div>

      <Handle
        type="target"
        position={Position.Left}
        style={{ width: 7, height: 7, background: profile.accent, border: '2px solid var(--surface)' }}
      />
      <Handle
        type="source"
        position={Position.Right}
        style={{ width: 7, height: 7, background: profile.accent, border: '2px solid var(--surface)' }}
      />
    </div>
  )
})

ExplorationNodeCard.displayName = 'ExplorationNodeCard'
