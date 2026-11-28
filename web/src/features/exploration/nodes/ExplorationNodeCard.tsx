/**
 * 探索图节点卡片（唯一自定义节点，React Flow nodeTypes 注册名 'exploration'）。
 *
 * 固定 264×104（与 dagre 布局假设严格一致），全部缩放级别渲染同一结构。
 * kind 编码三层：全卡淡染底（远距离的面积色，打开即全图时的主识别通道）
 * → accent 图标（中距离）→ 中文标签（近处的精确语义）。
 * 文字一律中性 token：小号彩字在任何主题都难保 4.5:1。
 *
 * 点选节点后的三个状态由上层写进包装类（选中 / 来路上 / 无关），样式在 exploration.css；
 * 那里用到的类别色相经 CSS 变量 --exp-accent 取自这里。
 *
 * 线索（执行者踩通、还没走到结论的路径）不靠给卡片底色染色来标——观测本来就是淡蓝底，
 * 换一种淡底色远看仍像一张颜色略不同的观测卡。卡片保持类别本来的样子，四边加一条描边
 * （.exp-card-lead），右上角骑一串脚印（LeadMark）。脚印渲染在卡片外面：卡片裁切溢出内容，
 * 脚印要探出角上。两者都取线索自己的颜色（LEAD_ACCENT）。已关闭的线索脚印撤掉，右上角留
 * 一枚中性的「线索已关闭」。
 */

import { memo, type CSSProperties } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { cn } from '@/lib/utils'
import { kindProfile, leadHint, nodeChips, nodeTitle, LEAD_ACCENT } from '../kindTheme'
import type { ExplorationFlowNode } from '../layout'
import { LeadClosedChip, LeadMark } from '../LeadMark'

export const ExplorationNodeCard = memo(({ data }: NodeProps<ExplorationFlowNode>) => {
  const { node } = data
  const profile = kindProfile(node.kind)
  const Icon = profile.icon
  const title = nodeTitle(node) || '（无内容）'
  const chips = nodeChips(node)
  const openLead = !!chips.lead && !chips.lead.closed

  return (
    <>
      <div
        className={cn(
          'exp-card group relative flex h-26 w-[264px] flex-col overflow-hidden rounded-xl border border-border',
          'pl-3 shadow-xs transition-[background-color,border-color,box-shadow] duration-150',
          'hover:border-border-strong hover:shadow-md',
          node.state === 'running' && 'exp-card-running',
          openLead && 'exp-card-lead',
        )}
        style={
          {
            background: `color-mix(in srgb, ${profile.accent} 10%, var(--surface))`,
            '--exp-accent': profile.accent,
            '--exp-lead': LEAD_ACCENT,
          } as CSSProperties
        }
      >
        {/* 脚印占着右上角：头部右侧给它让出位置 */}
        <div className={cn('flex items-center gap-1.5 pt-2', openLead ? 'pr-9' : 'pr-3')}>
          <Icon className="h-3.5 w-3.5 flex-shrink-0" style={{ color: profile.accent }} />
          <span className="text-2xs font-semibold tracking-wider text-text-secondary">{profile.label}</span>
          {chips.priority && (
            <span
              className={cn('ml-auto rounded bg-surface-2 px-1 py-px font-mono text-2xs font-semibold', chips.priority.className)}
              title="优先级（P0 关键 / P1 高 / P2 中 / P3 低）"
            >
              {chips.priority.code}
            </span>
          )}
          {chips.lead?.closed && (
            <LeadClosedChip title={chips.lead.detail || undefined} className={!chips.priority ? 'ml-auto' : undefined} />
          )}
        </div>

        <p className="mt-1 line-clamp-2 pr-3 text-xs leading-[18px] text-text" title={title}>
          {title}
        </p>

        {/* 徽章行统一语法：可选色点 + 中性文字——色相只落在点上，文字永可读 */}
        <div className="mt-auto flex items-center gap-1.5 overflow-hidden pb-2 pr-3 text-2xs text-text-secondary">
          {chips.state && (
            <span className="inline-flex flex-shrink-0 items-center gap-1">
              <span className={cn('h-1.5 w-1.5 rounded-full', chips.state.dotClass)} />
              {chips.state.label}
            </span>
          )}
          {chips.category && <span className="flex-shrink-0">{chips.category}</span>}
          {chips.verification && <span className="flex-shrink-0">{chips.verification}</span>}
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
      {openLead && <LeadMark title={leadHint({ closed: false, detail: '', spec: chips.lead!.spec })} />}
    </>
  )
})

ExplorationNodeCard.displayName = 'ExplorationNodeCard'
