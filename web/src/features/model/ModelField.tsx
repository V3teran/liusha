import { useId, useRef, useState } from 'react'
import { Loader2, RefreshCw } from 'lucide-react'
import { errorText } from '@/api/client'
import type { ProviderConfig, ProviderModelList } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Combobox, type ComboboxHandle } from '@/components/ui/Combobox'
import { FIELD_LABEL_CLASS } from '@/components/ui/control'
import { InlineError } from '@/components/ui/InlineError'
import { cn } from '@/lib/utils'
import { connectionKey, probeTarget, probeTargetMissing } from './providerRules'
import { useListProviderModels } from './useModelQueries'

/** 一次拉取的结论，连同它所属的连接：连接变了（协议、地址、密钥），结论随之作废。 */
type Fetched = Pick<ProviderModelList, 'models' | 'status' | 'detail'> & { key: string; request?: string }

// 默认模型：手填，或点「拉取模型列表」取回上游的可选模型后从下拉中选（可编辑下拉，展开即
// 列出全部）。拉取只填充下拉，不改写表单里的任何字段——鉴权方式、输出上限等由测试连接实测回填。
// 端点不提供列表时只能手填：这不是错误，很多兼容端点只实现对话接口。
// 不用 Field：它以 <label> 包住控件，展开按钮嵌在 label 里会破坏点击与无障碍语义。
export function ModelField({
  draft,
  patch,
}: {
  draft: ProviderConfig
  patch: (p: Partial<ProviderConfig>) => void
}) {
  const inputId = useId()
  const noteId = useId()
  const combobox = useRef<ComboboxHandle>(null)
  const fetchButton = useRef<HTMLButtonElement>(null)
  const listModels = useListProviderModels()
  const [fetched, setFetched] = useState<Fetched | null>(null)

  const key = connectionKey(draft)
  const current = fetched?.key === key ? fetched : null

  const pending = listModels.isPending
  const fetchModels = () => {
    if (pending) return
    const missing = probeTargetMissing(draft)
    if (missing) {
      setFetched({ key, models: [], status: 'fail', detail: missing })
      return
    }
    listModels.mutate(probeTarget(draft), {
      onSuccess: (res) => {
        setFetched({ key, ...res })
        // 焦点仍在拉取按钮上才展开：等待期间转去填别的字段时不抢焦点
        if (res.models.length > 0 && document.activeElement === fetchButton.current) combobox.current?.open()
      },
      onError: (e) => setFetched({ key, models: [], status: 'fail', detail: errorText(e, '拉取模型列表失败') }),
    })
  }

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={inputId} className={FIELD_LABEL_CLASS}>
        默认模型
      </label>
      <div className="flex items-center gap-2">
        <div className="min-w-0 flex-1">
          <Combobox
            ref={combobox}
            id={inputId}
            value={draft.default_model}
            options={current?.models ?? []}
            onChange={(v) => patch({ default_model: v })}
            placeholder="model-name"
            toggleLabel="展开模型列表"
            aria-describedby={current ? noteId : undefined}
          />
        </div>
        {/* 拉取期间不禁用而是忽略重复点击：禁用会让按钮失去焦点，响应回来时就分不清
            用户是否已转去别处，也就无法决定要不要展开下拉。 */}
        <Button
          ref={fetchButton}
          variant="secondary"
          className="w-9 px-0"
          onClick={fetchModels}
          aria-busy={pending}
          aria-label="拉取模型列表"
          title="拉取模型列表"
        >
          {pending ? <Loader2 className="animate-spin" /> : <RefreshCw />}
        </Button>
      </div>
      {current && <FetchNote id={noteId} fetched={current} />}
    </div>
  )
}

// 拉取结论：取到了只报条数（下拉本身就是结果），旁路来源与「不提供列表」如实说明，失败带原因与实际请求。
function FetchNote({ id, fetched }: { id: string; fetched: Fetched }) {
  const request = fetched.status !== 'ok' && fetched.request && (
    <span className="block break-all font-mono text-2xs text-text-tertiary">{fetched.request}</span>
  )
  if (fetched.status === 'fail') {
    return (
      <div id={id}>
        <InlineError>
          {fetched.detail}
          {request}
        </InlineError>
      </div>
    )
  }
  return (
    <p
      id={id}
      role="status"
      className={cn('text-xs', fetched.status === 'warn' ? 'text-status-warning' : 'text-text-tertiary')}
    >
      {fetched.detail || `已拉取 ${fetched.models.length} 个模型`}
      {request}
    </p>
  )
}
