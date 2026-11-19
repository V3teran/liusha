import { useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { RotateCcw, X } from 'lucide-react'
import { errorText } from '@/api/client'
import { Button } from '@/components/ui/Button'

// 「已删除的内置」：技能页与场景页共用。内置条目被删除后升级不再补回，这里列出它们，可逐个恢复
// 成当前版本的出厂内容。没有已删除的内置条目时不显示入口。

export interface DeletedBuiltin {
  code: string
  name: string
  description: string
}

interface DeletedBuiltinsProps {
  /** 资源名（技能 / 场景），用于文案。 */
  noun: string
  items: DeletedBuiltin[]
  onRestore: (code: string) => Promise<unknown>
}

export function DeletedBuiltins({ noun, items, onRestore }: DeletedBuiltinsProps) {
  const [open, setOpen] = useState(false)
  const [restoring, setRestoring] = useState<string | null>(null)
  const [error, setError] = useState('')

  if (items.length === 0 && !open) return null

  const restore = async (code: string) => {
    setRestoring(code)
    setError('')
    try {
      await onRestore(code)
    } catch (e) {
      setError(errorText(e, '恢复失败'))
    } finally {
      setRestoring(null)
    }
  }

  return (
    <Dialog.Root
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        setError('')
      }}
    >
      <Dialog.Trigger asChild>
        <Button variant="secondary">已删除的内置（{items.length}）</Button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content className="tac-modal fixed left-1/2 top-1/2 flex max-h-[80vh] w-[520px] max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col rounded-xl border border-border bg-surface shadow-lg outline-none">
          <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
            <Dialog.Title className="text-sm font-semibold text-text">已删除的内置{noun}</Dialog.Title>
            <Dialog.Close className="rounded-md p-1 text-text-tertiary hover:bg-surface-2 hover:text-text" aria-label="关闭">
              <X className="h-4 w-4" />
            </Dialog.Close>
          </div>
          <Dialog.Description className="px-5 pt-3 text-xs text-text-secondary">
            删除的内置{noun}升级时不会自动补回。恢复后为当前版本的出厂内容。
          </Dialog.Description>
          <ul className="flex min-h-0 flex-col gap-2 overflow-y-auto px-5 py-3" tabIndex={0} aria-label={`已删除的内置${noun}`}>
            {items.length === 0 && <li className="py-6 text-center text-sm text-text-tertiary">没有已删除的内置{noun}</li>}
            {items.map((it) => (
              <li key={it.code} className="flex items-start justify-between gap-3 rounded-lg border border-border px-3 py-2.5">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-text">{it.name}</p>
                  <p className="mt-0.5 line-clamp-2 text-xs text-text-tertiary">{it.description}</p>
                </div>
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={restoring !== null}
                  onClick={() => void restore(it.code)}
                  aria-label={`恢复${noun}「${it.name}」`}
                >
                  <RotateCcw className="h-3.5 w-3.5" aria-hidden />
                  {restoring === it.code ? '恢复中…' : '恢复'}
                </Button>
              </li>
            ))}
          </ul>
          {error && <p className="px-5 pb-3 text-xs text-status-error">{error}</p>}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
