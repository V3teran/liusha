import type { Message } from '@/api/types'
import { classifyMessage } from '@/lib/messageKind'
import { clockTime, fullTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Avatar } from './cards/Avatar'
import { UserBubble } from './cards/UserBubble'
import { AssistantText } from './cards/AssistantText'
import { ReasoningCard } from './cards/ReasoningCard'
import { SpawnCard } from './cards/SpawnCard'
import { ToolCallCard } from './cards/ToolCallCard'
import { ToolResultCard } from './cards/ToolResultCard'
import { FindingCard } from './cards/FindingCard'
import { CompactionCard } from './cards/CompactionCard'

interface MessageItemProps {
  msg: Message
  step?: number
}

// 卡片类型判定走共享分类器（lib/messageKind，与 ChatThread 分组逻辑同源）。
export function MessageItem({ msg, step }: MessageItemProps) {
  const kind = classifyMessage(msg)
  if (kind === 'hidden') return null

  const isUser = kind === 'user'
  // 叙述类（user / assistant / 推理）带头像；过程类（工具/结果/派发/漏洞）缩进对齐、不重复头像。
  const showAvatar = kind === 'user' || kind === 'assistant' || kind === 'reasoning'

  return (
    <div className={cn('flex items-start gap-2.5', isUser && 'flex-row-reverse')}>
      <div className="w-[30px] flex-shrink-0">
        {showAvatar && <Avatar who={isUser ? 'user' : 'agent'} />}
      </div>
      <div className={cn('flex min-w-0 flex-1 flex-col', isUser && 'items-end')}>
        {kind === 'user' && <UserBubble content={msg.Content} />}
        {kind === 'assistant' && <AssistantText content={msg.Content} />}
        {kind === 'reasoning' && (
          <ReasoningCard
            text={msg.Metadata!.Text || msg.Content}
            agentName={msg.Metadata!.AgentName}
            step={step}
            inTokens={msg.Metadata!.InTokens}
            outTokens={msg.Metadata!.OutTokens}
            latencyMs={msg.Metadata!.LatencyMs}
          />
        )}
        {kind === 'spawn' && <SpawnCard args={msg.Metadata!.Args} />}
        {kind === 'spawn-done' && (
          <SpawnCard done durationMs={msg.Metadata!.DurationMs} err={msg.Metadata!.Err} />
        )}
        {kind === 'tool-call' && (
          <ToolCallCard
            tool={msg.Metadata!.ToolName}
            args={msg.Metadata!.Args}
            agentName={msg.Metadata!.AgentName}
          />
        )}
        {kind === 'finding' && <FindingCard args={msg.Metadata!.Args} />}
        {kind === 'compaction' && (
          <CompactionCard label={msg.Metadata!.Result} summary={msg.Metadata!.Text} />
        )}
        {kind === 'tool-result' && (
          <ToolResultCard
            tool={msg.Metadata!.ToolName}
            result={msg.Metadata!.Result}
            durationMs={msg.Metadata!.DurationMs}
            err={msg.Metadata!.Err}
            agentName={msg.Metadata!.AgentName}
          />
        )}
        {msg.CreatedAt && (
          <time
            className="mt-1 px-1 font-mono text-[11px] leading-none text-faint"
            dateTime={msg.CreatedAt}
            title={fullTime(msg.CreatedAt)}
          >
            {clockTime(msg.CreatedAt)}
          </time>
        )}
      </div>
    </div>
  )
}
