import { Settings2 } from 'lucide-react'
import type { ToolCallView } from '@/lib/toolCalls'

interface ToolCallCardProps {
  call: ToolCallView
}

// 单个工具调用卡片：工具名标题 + 美化参数。
// 取代裸 JSON.stringify——审计/调试时一眼看清「调了哪个工具、传了什么参数」。
export function ToolCallCard({ call }: ToolCallCardProps) {
  return (
    <div className="mb-2 overflow-hidden rounded-lg border border-border last:mb-0">
      <div className="flex items-center gap-1.5 border-b border-border bg-accent/[0.09] px-2.5 py-1.5">
        <Settings2 className="h-3 w-3 flex-shrink-0 text-accent/70" aria-hidden="true" />
        <span className="font-mono text-[12.5px] font-semibold text-accent">{call.name}</span>
      </div>
      {call.args ? (
        <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words bg-surface-2 px-2.5 py-2 font-mono text-[11.5px] leading-relaxed text-text">
          {call.args}
        </pre>
      ) : (
        <span className="block px-2.5 py-1.5 text-[11.5px] text-muted opacity-70">无参数</span>
      )}
    </div>
  )
}
