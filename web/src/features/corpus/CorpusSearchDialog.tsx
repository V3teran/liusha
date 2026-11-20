import { useId, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { useMutation } from '@tanstack/react-query'
import { Search, X } from 'lucide-react'
import { errorText } from '@/api/client'
import { searchCorpus, type CorpusSearchResult } from '@/api/corpus'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { INPUT_CLASS } from '@/components/ui/control'
import { corpusScopeLabel, corpusSourceColor, corpusSourceLabel } from '@/lib/corpus'
import { CorpusScopeSelect } from './CorpusScopeSelect'

interface CorpusSearchDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onOpenEntry: (id: string) => void
}

function modeLine(r: CorpusSearchResult): string {
  if (r.fallback) return `字面检索 · 语义检索暂不可用：${r.fallback}`
  return r.mode === 'semantic' ? '语义检索 · 按重排相关度过滤' : '字面检索 · 按词元覆盖率过滤'
}

// 检索预览：以某个场景下的任务视角，执行与智能体 search_corpus 相同的检索——看一条查询
// 实际会拿到哪些知识、各自的得分。
export function CorpusSearchDialog({ open, onOpenChange, onOpenEntry }: CorpusSearchDialogProps) {
  const [query, setQuery] = useState('')
  const [scenario, setScenario] = useState<string | null>(null)
  const search = useMutation({ mutationFn: () => searchCorpus(query.trim(), scenario ?? '') })
  const queryId = useId()
  const scopeId = useId()
  const result = search.data

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content className="tac-modal fixed left-1/2 top-[8vh] flex max-h-[84vh] w-[680px] max-w-[calc(100vw-32px)] -translate-x-1/2 flex-col rounded-xl border border-border bg-surface shadow-lg outline-none">
          <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
            <Dialog.Title className="text-sm font-semibold text-text">检索预览</Dialog.Title>
            <Dialog.Description className="sr-only">以任务视角执行与智能体相同的知识库检索</Dialog.Description>
            <Dialog.Close asChild>
              <button
                type="button"
                aria-label="关闭"
                className="rounded-md p-1 text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text"
              >
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </div>

          <form
            className="flex flex-wrap items-end gap-3 border-b border-border px-5 py-4"
            onSubmit={(e) => {
              e.preventDefault()
              if (query.trim()) search.mutate()
            }}
          >
            <div className="flex min-w-[12rem] flex-1 flex-col gap-1.5">
              <label htmlFor={queryId} className="text-xs font-medium text-text-secondary">
                查询
              </label>
              <input
                id={queryId}
                autoFocus
                className={INPUT_CLASS}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>
            <div className="flex w-44 flex-col gap-1.5">
              <label htmlFor={scopeId} className="text-xs font-medium text-text-secondary">
                任务场景
              </label>
              <CorpusScopeSelect id={scopeId} value={scenario} onChange={setScenario} emptyLabel="无场景" />
            </div>
            <Button type="submit" disabled={!query.trim() || search.isPending}>
              <Search />
              {search.isPending ? '检索中…' : '检索'}
            </Button>
          </form>

          <div className="min-h-[8rem] flex-1 overflow-y-auto px-5 py-4" aria-live="polite">
            {search.isError ? (
              <p role="alert" className="text-sm text-sev-critical">
                ⚠ {errorText(search.error, '检索失败')}
              </p>
            ) : result ? (
              <div className="flex flex-col gap-3">
                <p className="text-xs text-text-tertiary">{modeLine(result)}</p>
                {result.hits.length === 0 ? (
                  <p className="py-6 text-center text-sm text-text-secondary">没有相关的知识</p>
                ) : (
                  <ol className="flex flex-col gap-2">
                    {result.hits.map((h) => (
                      <li key={h.entry.id}>
                        <button
                          type="button"
                          onClick={() => onOpenEntry(h.entry.id)}
                          className="flex w-full flex-col gap-1.5 rounded-lg border border-border px-3 py-2.5 text-left transition-colors hover:border-border-strong hover:bg-surface-2"
                        >
                          <span className="flex items-center gap-2">
                            <span className="font-mono text-xs tabular-nums text-text-secondary" title="得分">
                              {h.score.toFixed(2)}
                            </span>
                            <span className="min-w-0 flex-1 truncate text-sm font-medium text-text">{h.entry.title}</span>
                            <Badge dot={false} size="sm" color={corpusSourceColor(h.entry.source)}>
                              {corpusSourceLabel(h.entry.source)}
                            </Badge>
                          </span>
                          <span className="line-clamp-2 text-xs leading-relaxed text-text-secondary">{h.entry.content}</span>
                          <span className="text-[11px] text-text-tertiary">{corpusScopeLabel(h.entry.scenario)}</span>
                        </button>
                      </li>
                    ))}
                  </ol>
                )}
              </div>
            ) : null}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
