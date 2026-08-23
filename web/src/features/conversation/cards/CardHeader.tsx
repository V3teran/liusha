import type { ReactNode } from 'react'

// 卡内领头标题字号/字重（与用户指令块「指令」标签一致）。
export const TITLE_CLASS = 'text-[11px] font-semibold tracking-wide'

interface CardHeaderProps {
  /** 领头 agent 名（编排/侦察/利用/流量分析）——以其配色显示，一眼归属。 */
  agent?: string
  /** 领头 agent 配色（与卡外图标同色）。 */
  agentColor: string
  /** 动作词（推理/派发/漏洞…）——弱化在名字之后。 */
  action: string
  /** 派发目标 agent（如 编排 → 侦察），以目标 agent 配色显示。 */
  target?: { name: string; color: string }
  /** 尾随元信息（step/token/耗时 chip 等）。 */
  extras?: ReactNode
}

// 卡内领头标题（方案 B）：每个节点=「某 agent 在做某事」，故 agent 名领头（agent 色，
// 与卡外图标同色），动作词弱化其后；派发再接「→ 目标 agent」。消除旧标题「推理 编排 /
// 派发 → 侦察」里灰底 chip 主语漂移（编排=施动 vs 侦察=目标）的歧义。
export function CardHeader({ agent, agentColor, action, target, extras }: CardHeaderProps) {
  return (
    <div className="mb-1 flex flex-wrap items-center gap-1.5">
      {agent && (
        <span className={TITLE_CLASS} style={{ color: agentColor }}>
          {agent}
        </span>
      )}
      <span className="text-[11px] font-medium text-muted">{action}</span>
      {target && (
        <>
          <span className="text-faint">→</span>
          <span className={TITLE_CLASS} style={{ color: target.color }}>
            {target.name}
          </span>
        </>
      )}
      {extras}
    </div>
  )
}
