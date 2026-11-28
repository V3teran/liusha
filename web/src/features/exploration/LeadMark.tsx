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
 * 已走过的线索：关闭类别属于被处理过的（走到头、有结论、干涸、受阻、被否决）。脚印保留但
 * 从线索的暖橙换成正文色——它不再是「还没走到头」的呼喊，而是「这条路走过了」的留痕；描边
 * 还原为普通事实卡。没被处理就关闭的线索（不值得走）没有标记，类别与理由在详情面板。
 */
export function LeadMarkFollowed({ title, className, ...rest }: HTMLAttributes<HTMLSpanElement>) {
  return (
    <span
      {...rest}
      title={title}
      className={cn('exp-lead-mark inline-flex h-6 w-6 flex-shrink-0 items-center justify-center text-text', className)}
    >
      <Footprints aria-hidden fill="currentColor" className="h-6 w-6" strokeWidth={2} />
      <span className="sr-only">线索（已走过）</span>
    </span>
  )
}
