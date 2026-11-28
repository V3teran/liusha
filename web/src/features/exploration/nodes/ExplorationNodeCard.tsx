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
 * 线索的标记有三态（线索是事实观察带的高价值延伸说明：意外发现、有希望带来重大进展的方向）：
 * 开着（待深挖）——四边描边（.exp-card-lead）+ 右上角橙色脚印（LeadMark）；被跟进过且已关闭——
 * 脚印换中性灰（LeadMarkFollowed），描边还原，「这条路走过了」的留痕；未被跟进就关闭——无任何
 * 标记，不值得走的东西不留脚印。脚印渲染在卡片外面：卡片裁切溢出内容，脚印要探出角上。两者
 * 都取自己的颜色：开着的取线索的 LEAD_ACCENT，走过的取中性 token。关闭的类别与理由在详情面板。
 */

import { memo, type CSSProperties } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { cn } from '@/lib/utils'
import { kindProfile, leadHint, nodeChips, nodeTitle, LEAD_ACCENT } from '../kindTheme'
import type { ExplorationFlowNode } from '../layout'
import { LeadMarkFollowed, LeadMark } from '../LeadMark'

export const ExplorationNodeCard = memo(({ data }: NodeProps<ExplorationFlowNode>) => {
  const { node } = data
  const profile = kindProfile(node.kind)
  const Icon = profile.icon
  const title = nodeTitle(node) || '（无内容）'
  const chips = nodeChips(node)
  const leadState = !chips.lead ? 'none' : !chips.lead.closed ? 'open' : data.leadFollowed ? 'followed' : 'none'

  return (
    <>
      <div
        className={cn(
          'exp-card group relative flex h-26 w-[264px] flex-col overflow-hidden rounded-xl border border-border',
          'pl-3 shadow-xs transition-[background-color,border-color,box-shadow] duration-150',
          'hover:border-border-strong hover:shadow-md',
          node.state === 'running' && 'exp-card-running',
          leadState === 'open' && 'exp-card-lead',
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
        <div className={cn('flex items-center gap-1.5 pt-2', leadState !== 'none' ? 'pr-9' : 'pr-3')}>
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
          {chips.urgent && (
            <span
              className="ml-auto rounded border border-border-strong px-1.5 py-px text-2xs font-semibold text-text"
              title="加急：规划者判定这一步不做、同批其余动作都做不成或白做；空位不够时先派"
            >
              加急
            </span>
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
      {leadState === 'open' && <LeadMark title={leadHint({ closed: false, detail: '', spec: chips.lead!.spec })} />}
      {leadState === 'followed' && <LeadMarkFollowed title={`已走过的线索（${chips.lead!.detail}）`} />}
    </>
  )
})

ExplorationNodeCard.displayName = 'ExplorationNodeCard'
