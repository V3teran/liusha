import { User } from 'lucide-react'
import { relativeTime, fullTime } from '@/lib/format'
import { CopyButton } from '@/components/CopyButton'
import { TITLE_CLASS } from './CardHeader'

interface CommandBlockProps {
  content: string
  createdAt?: string
  copied: boolean
  onCopy: () => void
}

export function CommandBlock({ content, createdAt, copied, onCopy }: CommandBlockProps) {
  return (
    <div className="flex justify-end pb-5">
      <div className="flex w-full max-w-3xl flex-col items-end">
        <div className="flex w-full gap-3">
          <div className="w-full rounded-2xl rounded-tr-md bg-accent-soft px-3.5 py-2.5">
            <div className={`mb-1 text-accent ${TITLE_CLASS}`}>指令</div>
            <div className="whitespace-pre-wrap break-words text-sm leading-relaxed text-text">{content}</div>
          </div>
          <span className="flex h-8 w-8 flex-none items-center justify-center rounded-full bg-accent/10">
            <User className="h-5 w-5 text-accent" strokeWidth={2} />
          </span>
        </div>
        <div className="mr-11 mt-1.5 flex items-center gap-2">
          {createdAt && (
            <time
              className="px-1 text-[11px] text-faint"
              dateTime={createdAt}
              title={fullTime(createdAt)}
            >
              {relativeTime(createdAt)}
            </time>
          )}
          <CopyButton copied={copied} onClick={onCopy} />
        </div>
      </div>
    </div>
  )
}
