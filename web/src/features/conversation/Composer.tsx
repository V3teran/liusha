import { useState } from 'react'
import { Send, Square, Loader2 } from 'lucide-react'
import { followUp, startChat } from '@/api/client'
import { useConversationStore } from '@/stores/conversation'
import { cn } from '@/lib/utils'

interface ComposerProps {
  convId?: string
  scanning?: boolean
  onStarted: (convID: string) => void
  onAppended: (afterSeq: number) => void
  onStop: () => void
}

/**
 * Composer - 消息输入组件
 *
 * 设计改进：
 * 1. 更现代的输入框样式（圆角、聚焦状态）
 * 2. 更清晰的按钮层次（主操作 vs 次要操作）
 * 3. 更友好的交互提示
 * 4. 更流畅的状态反馈
 */
export function Composer({ convId, scanning, onStarted, onAppended, onStop }: ComposerProps) {
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
    } catch (e) {
      const err = e as Error & { busy?: boolean }
      setBusyMsg(err.busy ? '扫描进行中，先点停止再发' : '发送失败')
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

  const placeholder = convId
    ? '继续提问 / 追加指令…'
    : '描述要扫的目标 / 任务（URL、账号、测试方向）…'

  const canSend = brief.trim() && !sending

  return (
    <div className="flex flex-shrink-0 flex-col gap-3 border-t border-border bg-surface px-5 py-4">
      {/* 顶部提示栏 */}
      <div className="flex items-center justify-between">
        <span className="text-xs text-text-tertiary">
          <kbd className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px]">Enter</kbd> 发送 ·
          <kbd className="ml-1.5 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10px]">Shift+Enter</kbd> 换行
        </span>
        {busyMsg && (
          <span className="text-xs font-medium text-status-error animate-slide-down">
            {busyMsg}
          </span>
        )}
      </div>

      {/* 输入区域 */}
      <div
        className={cn(
          'rounded-xl border-2 bg-background transition-all duration-200',
          'focus-within:border-accent focus-within:glow-accent',
          busyMsg ? 'border-status-error' : 'border-border'
        )}
      >
        <textarea
          value={brief}
          onChange={(e) => setBrief(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder={placeholder}
          disabled={sending}
          className={cn(
            'w-full min-h-[72px] max-h-[240px] resize-y bg-transparent px-4 pt-3.5 pb-2',
            'text-sm leading-relaxed text-text placeholder:text-text-tertiary',
            'outline-none disabled:opacity-50 disabled:cursor-not-allowed'
          )}
        />

        {/* 底部操作栏 */}
        <div className="flex items-center justify-between gap-3 border-t border-border px-3 py-2.5">
          {/* 字数统计 */}
          <span className="text-xs text-text-tertiary tabular-nums">
            {brief.length > 0 && `${brief.length} 字符`}
          </span>

          {/* 操作按钮组 */}
          <div className="flex items-center gap-2">
            {/* 停止扫描按钮（仅扫描中显示）*/}
            {convId && scanning && (
              <button
                type="button"
                onClick={onStop}
                className={cn(
                  'inline-flex items-center gap-1.5 rounded-lg border-2 border-status-error px-3.5 py-2',
                  'text-xs font-semibold text-status-error',
                  'transition-all hover:bg-status-error/10',
                )}
              >
                <Square className="h-3.5 w-3.5 fill-current" />
                停止扫描
              </button>
            )}

            {/* 发送按钮 */}
            <button
              type="button"
              disabled={!canSend}
              onClick={() => void send()}
              className={cn(
                'inline-flex items-center gap-2 rounded-lg px-4 py-2',
                'text-sm font-semibold transition-all',
                canSend
                  ? 'bg-accent text-white hover:bg-accent-hover hover:shadow-lg'
                  : 'bg-surface-2 text-text-disabled cursor-not-allowed'
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
    </div>
  )
}
