import type { ReactNode } from 'react'
import { relativeTime, fullTime } from '@/lib/format'
import { agentProfile } from '@/lib/agentColor'
import { CopyButton } from '@/components/CopyButton'
import { cn } from '@/lib/utils'

interface AgentCardProps {
  agentName?: string
  createdAt?: string
  copied?: boolean
  onCopy?: () => void
  copyText?: string
  children: ReactNode
  className?: string
}

/**
 * AgentCard - AI 智能体消息卡片
 *
 * 设计理念：
 * 1. 清晰的身份标识：头像色环 + 角色名
 * 2. 分层的视觉层次：头像突出、内容收敛
 * 3. 渐进的信息披露：时间戳/操作按钮悬停可见
 */
export function AgentCard({
  agentName,
  createdAt,
  copied = false,
  onCopy,
  copyText,
  children,
  className,
}: AgentCardProps) {
  const profile = agentProfile(agentName)
  const Icon = profile.icon
  const showCopy = !!copyText && !!onCopy
  const showFooter = !!createdAt || showCopy

  return (
    <div className="group flex justify-start pb-6 animate-slide-up">
      <div className="flex w-full max-w-4xl flex-col">
        {/* 主体：头像 + 内容气泡 */}
        <div className="flex gap-3">
          {/* 智能体头像：色环 + 图标。28/16 对齐全站紧凑身份位
              （会话列表操作钮、配置页身份 chip），对话流是高密度时间线，
              头像要能认人但不与正文争宽度。 */}
          <div
            className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full ring-1 ring-inset transition-shadow group-hover:ring-2"
            style={{
              color: profile.accent,
              background: profile.soft,
              // @ts-ignore - CSS custom property
              '--tw-ring-color': profile.accent + '40',
            } as React.CSSProperties}
          >
            <Icon className="h-4 w-4" strokeWidth={2.2} />
          </div>

          {/* 内容气泡 */}
          <div
            className={cn(
              'min-w-0 flex-1 rounded-xl rounded-tl-md bg-surface px-4 py-3',
              'border border-transparent transition-colors',
              'group-hover:border-border-subtle',
              className
            )}
          >
            {children}
          </div>
        </div>

        {/* 元信息：时间戳 + 操作（常亮——渐进披露的悬停成本是"看不到有操作存在"）。
            ml-10 = 头像 28px + gap 12px，与气泡左缘对齐。 */}
        {showFooter && (
          <div className="ml-10 mt-1.5 flex items-center gap-2">
            {createdAt && (
              <time
                className="px-1 text-xs text-text-tertiary tabular-nums"
                dateTime={createdAt}
                title={fullTime(createdAt)}
              >
                {relativeTime(createdAt)}
              </time>
            )}
            {showCopy && (
              <CopyButton
                copied={copied}
                onClick={onCopy}
                size="sm"
              />
            )}
          </div>
        )}
      </div>
    </div>
  )
}
