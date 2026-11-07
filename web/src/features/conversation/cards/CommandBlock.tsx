import { Download, User } from 'lucide-react'
import { attachmentDownloadURL } from '@/api/attachments'
import type { AttachmentRef } from '@/api/types'
import { humanBytes, relativeTime, fullTime } from '@/lib/format'
import { CopyButton } from '@/components/CopyButton'
import { TITLE_CLASS } from './CardHeader'

interface CommandBlockProps {
  content: string
  createdAt?: string
  /** 随消息提交的附件（metadata.attachments）：芯片点开即下载（工作区 raw 通道）。 */
  attachments?: AttachmentRef[]
  copied: boolean
  onCopy: () => void
}

export function CommandBlock({ content, createdAt, attachments, copied, onCopy }: CommandBlockProps) {
  return (
    <div className="flex justify-end pb-5">
      <div className="flex w-full max-w-3xl flex-col items-end">
        <div className="flex w-full gap-3">
          <div className="w-full rounded-2xl rounded-tr-md bg-accent-soft px-4 py-2.5">
            <div className={`mb-1 text-accent ${TITLE_CLASS}`}>指令</div>
            {content && (
              <div className="whitespace-pre-wrap break-words text-sm leading-relaxed text-text">{content}</div>
            )}
            {attachments && attachments.length > 0 && (
              <div className={content ? 'mt-2 flex flex-wrap gap-1.5' : 'flex flex-wrap gap-1.5'}>
                {attachments.map((a) => (
                  <a
                    key={a.path}
                    href={attachmentDownloadURL(a.path)}
                    download={a.name}
                    title={`下载 ${a.name}（${humanBytes(a.size)}）`}
                    className="inline-flex max-w-[240px] items-center gap-1.5 rounded-lg border border-accent/20 bg-background/60 px-2 py-1 text-xs text-accent transition-colors hover:border-accent hover:bg-accent/10"
                  >
                    <Download className="h-3 w-3 flex-shrink-0" />
                    <span className="truncate">{a.name}</span>
                    <span className="flex-shrink-0 tabular-nums opacity-75">{humanBytes(a.size)}</span>
                  </a>
                ))}
              </div>
            )}
          </div>
          <span className="flex h-8 w-8 flex-none items-center justify-center rounded-full bg-accent/10">
            <User className="h-5 w-5 text-accent" strokeWidth={2} />
          </span>
        </div>
        <div className="mr-11 mt-1.5 flex items-center gap-2">
          {createdAt && (
            <time
              className="px-1 text-xs text-text-tertiary"
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
