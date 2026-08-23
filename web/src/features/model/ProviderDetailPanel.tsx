import { useState } from 'react'
import { CheckCircle2, Loader2, PlugZap, XCircle } from 'lucide-react'
import type { ProviderConfig } from '@/api/types'
import { testProvider } from '@/api/models'
import { Badge } from '@/components/ui/badge'
import { TYPE_LABEL, ProviderFormBody } from './providerForm'

interface ProviderDetailPanelProps {
  draft: ProviderConfig
  isNew: boolean
  saving: boolean
  saveDisabled: boolean
  onPatch: (p: Partial<ProviderConfig>) => void
  onSave: () => void
  onDelete?: () => void
}

// 测试连接结果的本地态：闲置 / 进行中 / 成功（含延迟）/ 失败（含中文原因）。
type TestState =
  | { kind: 'idle' }
  | { kind: 'testing' }
  | { kind: 'ok'; latencyMs: number; model: string }
  | { kind: 'fail'; msg: string }

// 右侧常驻详情面板：左列表点选/新建后原地渲染，不弹模态——同屏对照列表与表单。
// 单页分组表单（连接/能力）替代原二级 Radix Tabs：字段量不大，一屏滚动比点 tab 找字段更快。
// 底部工具条：测试连接（发最小 chat 验证 base_url+key+model）+ 删除 + 保存。
// 父组件按 key={isNew ? 'new' : draft.key} 挂载本组件，切换选中项时整体重挂载，测试态自然重置。
export function ProviderDetailPanel({
  draft,
  isNew,
  saving,
  saveDisabled,
  onPatch,
  onSave,
  onDelete,
}: ProviderDetailPanelProps) {
  const [test, setTest] = useState<TestState>({ kind: 'idle' })

  const onTest = async () => {
    setTest({ kind: 'testing' })
    try {
      const res = await testProvider({
        key: draft.key || undefined,
        type: draft.type,
        base_url: draft.base_url,
        default_model: draft.default_model,
        api_key: draft.api_key || undefined,
      })
      setTest(
        res.ok
          ? { kind: 'ok', latencyMs: res.latency_ms, model: res.model }
          : { kind: 'fail', msg: res.err_msg || '连接失败' },
      )
    } catch (e) {
      setTest({ kind: 'fail', msg: e instanceof Error ? e.message : '测试请求失败' })
    }
  }

  // 测试连接前置条件：base_url + default_model 必填，且能定位一把钥（新填明文或已存密钥）。
  const testDisabled =
    test.kind === 'testing' ||
    !draft.base_url ||
    !draft.default_model ||
    (!draft.api_key && !draft.key_present)

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between gap-4 border-b border-border px-6 py-4">
        <div className="min-w-0">
          <h2 className="tac-prompt truncate font-mono text-[15px] font-semibold text-text">
            {isNew ? '接入 provider' : draft.key}
          </h2>
          <p className="mt-0.5 truncate text-[12.5px] text-muted">
            {isNew ? '新增一个 provider 部署' : `${TYPE_LABEL[draft.type]} · ${draft.default_model}`}
          </p>
        </div>
        <div className="flex flex-shrink-0 items-center gap-2">
          {!draft.enabled && <Badge variant="outline">已停用</Badge>}
          {!isNew && !draft.key_present && (
            <Badge variant="outline" className="border-sev-high/50 text-sev-high">
              密钥未注入
            </Badge>
          )}
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4.5">
        <ProviderFormBody draft={draft} patch={onPatch} isNew={isNew} />
      </div>

      {/* 测试结果回显条：与保存/删除同区，紧贴工具条上方，占位稳定不跳动。 */}
      {test.kind !== 'idle' && (
        <div className="flex-shrink-0 border-t border-border px-6 pt-3">
          <TestResultLine test={test} />
        </div>
      )}

      <div className="flex flex-shrink-0 items-center gap-2.5 px-6 py-4">
        <button
          type="button"
          onClick={() => void onTest()}
          disabled={testDisabled}
          title={testDisabled ? '需先填 Base URL、默认模型，并有可用密钥' : '发一条最小请求验证连接'}
          className="flex items-center gap-1.5 rounded-lg border border-border px-3.5 py-1.5 text-[13px] text-muted transition-colors hover:border-accent hover:text-accent disabled:cursor-default disabled:opacity-40"
        >
          {test.kind === 'testing' ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <PlugZap className="h-3.5 w-3.5" />
          )}
          测试连接
        </button>
        {onDelete && (
          <button
            type="button"
            onClick={onDelete}
            disabled={saving}
            className="rounded-lg border border-sev-critical/40 px-4 py-1.5 text-[13px] text-sev-critical transition-colors hover:bg-sev-critical/10 disabled:cursor-default disabled:opacity-40"
          >
            删除
          </button>
        )}
        <button
          type="button"
          onClick={onSave}
          disabled={saving || saveDisabled}
          className="ml-auto rounded-lg bg-accent px-5 py-1.5 text-[13px] text-white transition-all hover:bg-accent-hover hover:shadow-[var(--glow-accent-strong)] disabled:cursor-default disabled:opacity-40 disabled:hover:shadow-none"
        >
          {saving ? '保存中…' : '保存'}
        </button>
      </div>
    </div>
  )
}

// 测试结果单行回显：成功绿（延迟 + model）、失败红（中文原因）、进行中转圈。
function TestResultLine({ test }: { test: TestState }) {
  if (test.kind === 'testing') {
    return (
      <span className="flex items-center gap-1.5 text-[12.5px] text-muted">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        正在验证连接…
      </span>
    )
  }
  if (test.kind === 'ok') {
    return (
      <span className="flex items-center gap-1.5 text-[12.5px] text-accent">
        <CheckCircle2 className="h-3.5 w-3.5" />
        连接成功 · {test.model} · {test.latencyMs}ms
      </span>
    )
  }
  if (test.kind === 'fail') {
    return (
      <span className="flex items-start gap-1.5 text-[12.5px] text-sev-critical">
        <XCircle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
        {test.msg}
      </span>
    )
  }
  return null
}
