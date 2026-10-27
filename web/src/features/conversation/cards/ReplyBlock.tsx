import { relativeTime, fullTime } from '@/lib/format'
import { agentProfile } from '@/lib/agentColor'
import { CopyButton } from '@/components/CopyButton'
import { CardHeader } from './CardHeader'
import { Markdown } from './Markdown'
import { cn } from '@/lib/utils'

interface ReplyBlockProps {
  content: string
  agentName?: string
  createdAt?: string
  copied: boolean
  onCopy: () => void
  className?: string
}

/**
 * ReplyBlock - AI 回复内容块
 */
export function ReplyBlock({ content, agentName, createdAt, copied, onCopy, className }: ReplyBlockProps) {
  const profile = agentProfile(agentName)

  return (
    <div
      className={cn(
        'group w-full rounded-xl rounded-tl-md border border-transparent bg-surface-2 px-4 py-3',
        'transition-colors duration-200 hover:border-border-subtle',
        className
      )}
    >
      <CardHeader
        agent={profile.label || undefined}
        agentColor={profile.accent}
        action="答复"
      />

      <Markdown
        content={content}
        className="mt-2 text-sm leading-relaxed text-text"
      />

      <div className="mt-3 flex items-center gap-2 opacity-0 transition-opacity duration-200 group-hover:opacity-100">
        {createdAt && (
          <time
            className="px-1 text-xs text-text-tertiary tabular-nums"
            dateTime={createdAt}
            title={fullTime(createdAt)}
          >
            {relativeTime(createdAt)}
          </time>
        )}
        <CopyButton copied={copied} onClick={onCopy} size="sm" />
      </div>
    </div>
  )
}
