import type { HTMLAttributes } from 'react'
import { Footprints } from 'lucide-react'
import { cn } from '@/lib/utils'
import { LEAD_ACCENT } from './kindTheme'

/**
 * 线索标记：执行者踩通、还没走到结论的路径，是图上最该一眼认出的东西。
 *
 * 一串实心脚印，骑在卡片右上角上（卡片的描边见 exploration.css 的 .exp-card-lead）。
 * 右上角在线索卡上是空的：线索只会是事实观察，事实不带优先级。脚印与描边都取线索自己的
 * 颜色（LEAD_ACCENT）。不靠给卡片底色染色——观测本来就是淡蓝底，换一种淡底色远看仍像一张
 * 颜色略有不同的观测卡。图例里的样例就是卡片上的实物。
 */
export function LeadMark({ title, className, ...rest }: HTMLAttributes<HTMLSpanElement>) {
  return (
    <span {...rest} title={title} className={cn('exp-lead-mark inline-flex h-6 w-6 flex-shrink-0 items-center justify-center', className)}>
      <Footprints aria-hidden fill="currentColor" className="h-6 w-6" strokeWidth={2} style={{ color: LEAD_ACCENT }} />
      <span className="sr-only">线索</span>
    </span>
  )
}

/**
 * 已关闭的线索：不再有人沿它走，脚印撤掉，卡片右上角留一枚中性的「线索已关闭」，记下这里
 * 曾有过一条线索。关闭的原因放进悬停说明（详情面板另有全文）。
 */
export function LeadClosedChip({ title, className }: { title?: string; className?: string }) {
  return (
    <span
      title={title}
      className={cn(
        'inline-flex flex-shrink-0 items-center gap-1 rounded-md border border-border-strong px-1.5 py-px text-2xs font-semibold text-text',
        className,
      )}
    >
      <Footprints aria-hidden className="h-3 w-3" />
      线索已关闭
    </span>
  )
}
