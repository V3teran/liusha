import { clockTime, fullTime } from '@/lib/format'
import { CopyButton } from '@/components/CopyButton'

interface CommandBlockProps {
  content: string
  createdAt?: string
  copied: boolean
  onCopy: () => void
}

// 用户指令块（方案 A）：右对齐独立块，与 AI 的左侧导轨节点分置——一眼分清「我下令」vs「AI 执行」。
// accent 软色底 + 「指令」标签，承担「这是指挥官的命令」语义；不进左侧导轨（那是 AI 作战轨迹）。
export function CommandBlock({ content, createdAt, copied, onCopy }: CommandBlockProps) {
  return (
    <div className="flex justify-end pb-[18px]">
      <div className="flex max-w-[80%] flex-col items-end">
        <div className="w-full rounded-2xl rounded-tr-md bg-accent-soft px-3.5 py-2.5">
          <div className="mb-1 text-[11px] font-semibold tracking-wide text-accent">指令</div>
          <div className="whitespace-pre-wrap break-words text-sm leading-relaxed text-text">{content}</div>
        </div>
        <div className="mt-1 flex items-center gap-1.5">
          {createdAt && (
            <time
              className="px-1 font-mono text-[11px] leading-none text-faint"
              dateTime={createdAt}
              title={fullTime(createdAt)}
            >
              {clockTime(createdAt)}
            </time>
          )}
          <CopyButton
            compact
            copied={copied}
            onClick={onCopy}
            className="opacity-60 transition-opacity hover:opacity-100"
          />
        </div>
      </div>
    </div>
  )
}
