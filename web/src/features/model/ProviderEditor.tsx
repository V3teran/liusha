import { useState } from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  PlugZap,
  RotateCcw,
  X,
  XCircle,
} from 'lucide-react'
import type { ProviderConfig } from '@/api/types'
import { testProvider } from '@/api/models'
import { Badge } from '@/components/ui/badge'
import { providerDisplayName, ProviderFormBody } from './providerForm'

interface ProviderEditorProps {
  draft: ProviderConfig
  isNew: boolean
  dirty: boolean
  saving: boolean
  saveDisabled: boolean
  actionError: string // 保存/删除失败的编辑面板内回显
  pendingIntent: 'collapse' | 'switch' | null // dirty 收起/切换确认条
  onPatch: (p: Partial<ProviderConfig>) => void
  onSave: () => void
  onReset: () => void
  onRestoreEnvKey?: () => void // 恢复使用环境变量（清密文，立即生效）
  onDiscard: () => void // 确认条「丢弃」：执行被拦的收起/切换
  onKeepEditing: () => void
  onDismissError: () => void
}

// 测试连接结果的本地态：闲置 / 进行中 / 成功（含延迟）/ 失败（含中文原因）。
type TestState =
  | { kind: 'idle' }
  | { kind: 'testing' }
  | { kind: 'ok'; latencyMs: number; model: string }
  | { kind: 'fail'; msg: string }

// 行内下拉展开的 provider 编辑面板：单页分组表单（连接/能力）+ 底部工具条。
// 工具条：测试连接（发最小 chat 验证）· 删除（两段式确认，仅编辑态）· 重置（dirty 时出现）· 保存。
// 保存/删除失败与 dirty 收起确认都在面板内回显；表单字段限宽保持可读密度。
export function ProviderEditor({
  draft,
  isNew,
  dirty,
  saving,
  saveDisabled,
  actionError,
  pendingIntent,
  onPatch,
  onSave,
  onReset,
  onRestoreEnvKey,
  onDiscard,
  onKeepEditing,
  onDismissError,
}: ProviderEditorProps) {
  const [test, setTest] = useState<TestState>({ kind: 'idle' })

  const onTest = async () => {
    // 不做禁用态静默拦截：缺前置时点击即给原因（可发现性优于灰按钮）。
    if (!draft.base_url || !draft.default_model || (!draft.api_key && !draft.key_present)) {
      setTest({ kind: 'fail', msg: '需先填 Base URL、默认模型，并有可用密钥' })
      return
    }
    setTest({ kind: 'testing' })
    try {
      const res = await testProvider({
        code: draft.code || undefined,
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

  return (
    <div className="border-t border-border-subtle bg-surface-2/30 px-4 py-4">
      <div className="mb-3 flex items-center justify-between gap-3">
        <h3 className="text-sm font-semibold text-text">
          {isNew ? '新建 provider' : `编辑 ${providerDisplayName(draft)}`}
        </h3>
        {dirty && (
          <Badge variant="outline" className="border-accent/60 text-accent">
            未保存
          </Badge>
        )}
      </div>
      <div className="max-w-xl">
        <ProviderFormBody draft={draft} patch={onPatch} isNew={isNew} onRestoreEnvKey={onRestoreEnvKey} />
      </div>

      {/* dirty 收起确认条：丢弃 / 继续编辑。 */}
      {pendingIntent !== null && (
        <div className="mt-3 flex items-center gap-3 rounded-lg border border-sev-high/40 bg-sev-high/5 px-3.5 py-2.5">
          <AlertTriangle className="h-4 w-4 flex-shrink-0 text-sev-high" />
          <p className="min-w-0 flex-1 text-xs text-text">
            有未保存更改——{pendingIntent === 'switch' ? '切换到其他行' : '收起'}将丢弃这些更改
          </p>
          <button
            type="button"
            onClick={onKeepEditing}
            className="flex-shrink-0 rounded-md border border-border px-2.5 py-1 text-xs text-muted transition-colors hover:border-accent hover:text-accent"
          >
            继续编辑
          </button>
          <button
            type="button"
            onClick={onDiscard}
            className="flex-shrink-0 rounded-md border border-sev-high/50 px-2.5 py-1 text-xs text-sev-high transition-colors hover:bg-sev-high/10"
          >
            {pendingIntent === 'switch' ? '丢弃并切换' : '丢弃并收起'}
          </button>
        </div>
      )}

      {/* 保存/删除失败回显条。 */}
      {actionError && (
        <div className="mt-3 flex items-start gap-2 rounded-lg border border-sev-critical/40 bg-sev-critical/5 px-3.5 py-2.5">
          <XCircle className="mt-0.5 h-4 w-4 flex-shrink-0 text-sev-critical" />
          <p className="min-w-0 flex-1 break-all text-xs text-sev-critical">{actionError}</p>
          <button
            type="button"
            onClick={onDismissError}
            aria-label="关闭错误提示"
            className="flex-shrink-0 text-faint transition-colors hover:text-text"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      )}

      {/* 测试结果回显条。 */}
      {test.kind !== 'idle' && (
        <div className="mt-3">
          <TestResultLine test={test} />
        </div>
      )}

      <div className="mt-4 flex items-center gap-2.5">
        <button
          type="button"
          onClick={() => void onTest()}
          disabled={test.kind === 'testing'}
          title="发一条最小请求验证连接（需 Base URL、默认模型与可用密钥）"
          className="flex items-center gap-1.5 rounded-lg border border-border px-3.5 py-1.5 text-[13px] text-muted transition-colors hover:border-accent hover:text-accent disabled:cursor-default disabled:opacity-40"
        >
          {test.kind === 'testing' ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <PlugZap className="h-3.5 w-3.5" />
          )}
          测试连接
        </button>
        {dirty && (
          <button
            type="button"
            onClick={onReset}
            disabled={saving}
            title="放弃草稿，恢复到打开时的值"
            className="flex items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-xs text-muted transition-colors hover:border-accent hover:text-accent disabled:cursor-default disabled:opacity-40"
          >
            <RotateCcw className="h-3.5 w-3.5" />
            重置
          </button>
        )}
        <button
          type="button"
          onClick={onSave}
          disabled={saving || saveDisabled || !dirty}
          className="ml-auto rounded-lg bg-accent-soft px-5 py-1.5 text-[13px] font-semibold text-accent transition-colors hover:bg-accent/10 hover:text-accent-hover disabled:cursor-default disabled:opacity-40"
        >
          {saving ? '保存中…' : '保存'}
        </button>
      </div>
    </div>
  )
}

// 测试结果单行回显：成功（延迟 + model）、失败（中文原因）、进行中转圈。
function TestResultLine({ test }: { test: TestState }) {
  if (test.kind === 'testing') {
    return (
      <span className="flex items-center gap-1.5 text-xs text-muted">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
        正在验证连接…
      </span>
    )
  }
  if (test.kind === 'ok') {
    return (
      <span className="flex items-center gap-1.5 text-xs text-accent">
        <CheckCircle2 className="h-3.5 w-3.5" />
        连接成功 · {test.model} · {test.latencyMs}ms
      </span>
    )
  }
  if (test.kind === 'fail') {
    return (
      <span className="flex items-start gap-1.5 text-xs text-sev-critical">
        <XCircle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
        {test.msg}
      </span>
    )
  }
  return null
}
