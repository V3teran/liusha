import { useState } from 'react'
import { Send, Loader2 } from 'lucide-react'
import { followUp, startChat } from '@/api/conversations'
import { useConversationStore } from '@/stores/conversation'
import { cn } from '@/lib/utils'

interface ComposerProps {
  convId?: string
  /** 禁用态：框架仍在（聊天界面不随数据缺失消失），输入与发送关闭。 */
  disabled?: boolean
  /** 禁用态的输入框占位文案（缺省走通用文案）。 */
  placeholder?: string
  onStarted: (convID: string) => void
  onAppended: (afterSeq: number) => void
}

/**
 * Composer - 消息输入组件
 */
export function Composer({ convId, disabled = false, placeholder, onStarted, onAppended }: ComposerProps) {
  const lastSeq = useConversationStore((s) => s.lastSeq)
  const beginThinking = useConversationStore((s) => s.beginThinking)
  const [brief, setBrief] = useState('')
  const [busyMsg, setBusyMsg] = useState('')
  const [sending, setSending] = useState(false)

  const send = async () => {
    if (!brief.trim() || sending) return
    setBusyMsg('')
    setSending(true)
    try {
      if (convId) {
        const beforeSeq = lastSeq
        await followUp(convId, brief)
        setBrief('')
        beginThinking()
        onAppended(beforeSeq)
      } else {
        const { conversation_id } = await startChat(brief)
        setBrief('')
        beginThinking()
        onStarted(conversation_id)
      }
    } catch {
      setBusyMsg('发送失败')
    } finally {
      setSending(false)
    }
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void send()
    }
  }

  const effectivePlaceholder = placeholder ?? (convId
    ? '继续提问 / 追加指令…'
    : '描述要扫的目标 / 任务（URL、账号、测试方向）…')

  const canSend = brief.trim() && !sending && !disabled

  return (
    <div className="flex flex-shrink-0 flex-col gap-3 border-t border-border bg-surface px-5 py-4">
      {/* 输入区域：1px 边框（与全站 INPUT_CLASS/弹层同一权重，border-2 是体系外的异类），
          focus 走 accent + 辉光；错误态整体转红。 */}
      <div
        className={cn(
          'rounded-xl border bg-background transition-all duration-200',
          'focus-within:border-accent focus-within:glow-accent',
          busyMsg ? 'border-status-error' : 'border-border'
        )}
      >
        <textarea
          disabled={disabled || sending}
          aria-label="消息输入"
          value={brief}
          onChange={(e) => setBrief(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder={effectivePlaceholder}
          className={cn(
            'w-full min-h-[72px] max-h-[240px] resize-y bg-transparent px-4 pt-3.5 pb-2',
            'text-sm leading-relaxed text-text placeholder:text-text-tertiary',
            'outline-none disabled:opacity-50 disabled:cursor-not-allowed'
          )}
        />

        {/* 底部操作栏：状态一行收口，优先级 错误 > 字数 > 快捷键提示——快捷键提示
            与动作同排、贴近输入（业界惯例），不再悬在输入框上方占一行。 */}
        <div className="flex items-center justify-between gap-3 border-t border-border px-3 py-2.5">
          <div className="min-w-0 truncate text-xs text-text-tertiary">
            {busyMsg ? (
              <span className="animate-slide-down font-medium text-status-error">{busyMsg}</span>
            ) : brief.length > 0 ? (
              <span className="tabular-nums">{brief.length} 字符</span>
            ) : (
              <span>
                <kbd className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs">Enter</kbd> 发送 ·
                <kbd className="ml-1.5 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs">Shift+Enter</kbd> 换行
              </span>
            )}
          </div>

          {/* 发送按钮 */}
          <button
            type="button"
            disabled={disabled || !canSend}
            onClick={() => void send()}
            className={cn(
              'inline-flex items-center gap-2 rounded-lg px-4 py-2',
              'text-sm font-semibold transition-all',
              canSend
                ? 'cursor-pointer bg-accent-soft text-accent hover:bg-accent/10 hover:text-accent-hover'
                : 'cursor-not-allowed bg-surface-2 text-text-disabled'
            )}
          >
            {sending ? (
              <>
                <Loader2 className="h-4 w-4 animate-spin" />
                发送中
              </>
            ) : (
              <>
                <Send className="h-4 w-4" />
                发送
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  )
}
