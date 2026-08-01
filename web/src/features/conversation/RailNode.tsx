import type { LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

interface RailNodeProps {
  /** 导轨图标（lucide 线性）——语义分类索引：想=Brain、派发=Rocket、漏洞=Bug… */
  icon: LucideIcon
  /** 图标点主色（agent 色 / severity 色 / accent）。 */
  accent: string
  /** 图标点底色（agent soft / 中性）。 */
  soft: string
  /** 子代理节点：整体左移一档，用 agent 色连接线分组（仅子代理缩进，orchestrator 不缩进）。 */
  sub?: boolean
  /** 末节点：隐藏向下连接线。 */
  last?: boolean
  children: ReactNode
}

// 统一活动时间轴的节点容器（方案 A）：左侧固定图标导轨 + 内容槽。
// 「谁在干活」由图标点的色/图标承载；子代理用 agent 色连接线整体缩进分组，
// 一眼看出「编排在派活、侦察/利用在并行执行」。取代过去聊天气泡 + 图标徽章双系统。
export function RailNode({ icon: Icon, accent, soft, sub = false, last = false, children }: RailNodeProps) {
  return (
    <div className={cn('group grid gap-x-3', sub ? 'grid-cols-[14px_26px_1fr]' : 'grid-cols-[26px_1fr]')}>
      {sub && (
        <div className="flex justify-center">
          <span className="w-0.5 rounded-full" style={{ background: accent }} />
        </div>
      )}
      <div className="flex flex-col items-center">
        <span
          className="flex h-[26px] w-[26px] flex-none items-center justify-center rounded-lg"
          style={{ color: accent, background: soft }}
        >
          <Icon className="h-3.5 w-3.5" strokeWidth={2} />
        </span>
        {!last && <span className="my-1 w-0.5 flex-1 rounded-full bg-border" />}
      </div>
      <div className="min-w-0 pb-[18px]">{children}</div>
    </div>
  )
}
