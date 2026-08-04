import { clockTime, fullTime } from '@/lib/format'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { CopyButton } from '@/components/CopyButton'
import { CardHeader } from './CardHeader'
import { Markdown } from './Markdown'

interface ReplyBlockProps {
  content: string
  agentName?: string // 产出答复的 hunter（通常是编排）
  createdAt?: string
  copied: boolean
  onCopy: () => void
}

// agent 答复块（方案 B）：与用户指令块（CommandBlock）左右对称——用户在右、accent 软底、右上收角；
// agent 在左、中性 surface 底、左上收角。领头「hunter 答复」（hunter 色 + 弱化动作）与推理/派发卡
// 一致。答复是「对用户的最终回复」，是会话轮次的一方，故与用户指令平级左右分置。
export function ReplyBlock({ content, agentName, createdAt, copied, onCopy }: ReplyBlockProps) {
  const accent = agentAccent(agentName)
  const hunter = agentLabel(agentName)

  return (
    <div className="flex justify-start pb-[18px]">
      <div className="flex max-w-[80%] flex-col items-start">
        <div className="w-full rounded-2xl rounded-tl-md bg-surface-2 px-3.5 py-2.5">
          <CardHeader hunter={hunter || undefined} hunterColor={accent.accent} action="答复" />
          <Markdown content={content} className="text-sm leading-relaxed text-text" />
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
