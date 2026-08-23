import type { LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { clockTime, fullTime } from '@/lib/format'
import { CopyButton } from '@/components/CopyButton'
import { cn } from '@/lib/utils'

interface AgentCardProps {
  /** 动作分类图标（lucide 线性）：推理=Brain、派发=Rocket、漏洞=Bug、压缩=Archive。 */
  icon: LucideIcon
  /** 图标主色 = 该 agent 配色（与卡内领头名同色，一眼归属）。 */
  accent: string
  /** 图标底色 = 该 agent soft 色。 */
  soft: string
  /** 子代理卡：整体右缩一档，表达「从属于调度者」。 */
  sub?: boolean
  /** 脚注时间戳（与用户指令块一致）。 */
  createdAt?: string
  copied?: boolean
  onCopy?: () => void
  /** 有可复制正文时传入——脚注渲染复制按钮（与用户指令块一致）。 */
  copyText?: string
  children: ReactNode
}

// agent 活动卡（方案 B）：与用户指令块左右对称、材质等重。
//   - 图标在卡外左上：形状=动作分类，颜色=该 agent；
//   - surface 卡框包裹正文（推理/派发/漏洞…），左上收角与用户块右上收角镜像；
//   - 脚注时间 + 复制按钮，与用户指令块完全一致。
// 取代旧的「左侧裸文字挂图标导轨」——用户块有框、agent 无框的材质割裂就此消除。
export function AgentCard({
  icon: Icon,
  accent,
  soft,
  sub = false,
  createdAt,
  copied = false,
  onCopy,
  copyText,
  children,
}: AgentCardProps) {
  const showCopy = !!copyText && !!onCopy
  const showFooter = !!createdAt || showCopy
  return (
    <div className={cn('flex justify-start pb-[18px]', sub && 'pl-6')}>
      <div className="flex max-w-[85%] flex-col items-start">
        <div className="flex gap-2.5">
          <span
            className="mt-0.5 flex h-[26px] w-[26px] flex-none items-center justify-center rounded-lg"
            style={{ color: accent, background: soft }}
          >
            <Icon className="h-3.5 w-3.5" strokeWidth={2} />
          </span>
          <div className="min-w-0 rounded-2xl rounded-tl-md bg-surface px-3.5 py-2.5">{children}</div>
        </div>
        {showFooter && (
          <div className="ml-9 mt-1 flex items-center gap-1.5">
            {createdAt && (
              <time
                className="px-1 font-mono text-[11px] leading-none text-faint"
                dateTime={createdAt}
                title={fullTime(createdAt)}
              >
                {clockTime(createdAt)}
              </time>
            )}
            {showCopy && (
              <CopyButton
                compact
                copied={copied}
                onClick={onCopy}
                className="opacity-60 transition-opacity hover:opacity-100"
              />
            )}
          </div>
        )}
      </div>
    </div>
  )
}
