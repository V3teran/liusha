import { useEffect, useRef, useState } from 'react'
import { AlertTriangle, CheckCircle2, Loader2, PlugZap, RotateCcw, X, XCircle } from 'lucide-react'
import type { ProviderConfig } from '@/api/types'
import { testProvider } from '@/api/models'
import { Badge } from '@/components/ui/badge'
import { TYPE_LABEL, ProviderFormBody } from './providerForm'

interface ProviderDetailPanelProps {
  draft: ProviderConfig
  isNew: boolean
  dirty: boolean
  saving: boolean
  saveDisabled: boolean
  actionError: string // 保存/删除失败的页面内回显
  pendingSwitchLabel: string | null // dirty 拦截的切换目标名（确认条显示）
  onPatch: (p: Partial<ProviderConfig>) => void
  onSave: () => void
  onReset: () => void
  onDelete: () => void
  onDiscardAndSwitch: () => void
  onKeepEditing: () => void
  onDismissError: () => void
}

// 测试连接结果的本地态：闲置 / 进行中 / 成功（含延迟）/ 失败（含中文原因）。
type TestState =
  | { kind: 'idle' }
  | { kind: 'testing' }
  | { kind: 'ok'; latencyMs: number; model: string }
  | { kind: 'fail'; msg: string }

// 右侧常驻详情面板：左列表点选/新建后原地渲染，不弹模态——同屏对照列表与表单。
// 单页分组表单（连接/能力）限宽 560px 左对齐：设置表单的字段行不必拉满千像素宽栏。
// 底部工具条自左至右：测试连接 · 删除（两段式确认）· 重置（dirty 时出现）· 保存。
// 保存/删除失败与 dirty 切换拦截都在面板内回显（无 window.alert/confirm）。
// 父组件按 key={isNew ? 'new' : draft.key} 挂载本组件，切换选中项时整体重挂载，本地态自然重置。
export function ProviderDetailPanel({
  draft,
  isNew,
  dirty,
  saving,
  saveDisabled,
  actionError,
  pendingSwitchLabel,
  onPatch,
  onSave,
  onReset,
  onDelete,
  onDiscardAndSwitch,
  onKeepEditing,
  onDismissError,
}: ProviderDetailPanelProps) {
  const [test, setTest] = useState<TestState>({ kind: 'idle' })
  // 两段式删除：首击亮起「确认删除」，再击才真正删除；3 秒无操作自动回退防误触。
  const [confirmingDelete, setConfirmingDelete] = useState(false)
  const confirmTimer = useRef<number>(0)

  useEffect(() => () => window.clearTimeout(confirmTimer.current), [])

  const armDelete = () => {
    setConfirmingDelete(true)
    window.clearTimeout(confirmTimer.current)
    confirmTimer.current = window.setTimeout(() => setConfirmingDelete(false), 3000)
  }

  const onDeleteClick = () => {
    if (!confirmingDelete) {
      armDelete()
      return
    }
    window.clearTimeout(confirmTimer.current)
    setConfirmingDelete(false)
    onDelete()
  }

  const onTest = async () => {
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
            {isNew ? '接入 provider' : draft.code}
          </h2>
          <p className="mt-0.5 truncate text-[12.5px] text-muted">
            {isNew ? '新增一个 provider 部署' : `${TYPE_LABEL[draft.type]} · ${draft.default_model}`}
          </p>
        </div>
        <div className="flex flex-shrink-0 items-center gap-2">
          {dirty && (
            <Badge variant="outline" className="border-accent/60 text-accent">
              未保存
            </Badge>
          )}
          {!draft.enabled && <Badge variant="outline">已停用</Badge>}
          {!isNew && !draft.key_present && (
            <Badge variant="outline" className="border-sev-high/50 text-sev-high">
              密钥未注入
            </Badge>
          )}
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4.5">
        <div className="max-w-[560px]">
          <ProviderFormBody draft={draft} patch={onPatch} isNew={isNew} />
        </div>
      </div>

      {/* dirty 切换拦截确认条：丢弃并切换 / 继续编辑。 */}
      {pendingSwitchLabel !== null && (
        <div className="flex flex-shrink-0 items-center gap-3 border-t border-sev-high/40 bg-sev-high/5 px-6 py-3">
          <AlertTriangle className="h-4 w-4 flex-shrink-0 text-sev-high" />
          <p className="min-w-0 flex-1 text-[12.5px] text-text">
            有未保存更改——切换到「{pendingSwitchLabel}」将丢弃这些更改
          </p>
          <button
            type="button"
            onClick={onKeepEditing}
            className="flex-shrink-0 rounded-md border border-border px-2.5 py-1 text-[12px] text-muted transition-colors hover:border-accent hover:text-accent"
          >
            继续编辑
          </button>
          <button
            type="button"
            onClick={onDiscardAndSwitch}
            className="flex-shrink-0 rounded-md border border-sev-high/50 px-2.5 py-1 text-[12px] text-sev-high transition-colors hover:bg-sev-high/10"
          >
            丢弃并切换
          </button>
        </div>
      )}

      {/* 保存/删除失败回显条：页面内呈现，替代 window.alert。 */}
      {actionError && (
        <div className="flex flex-shrink-0 items-start gap-2 border-t border-sev-critical/40 bg-sev-critical/5 px-6 py-3">
          <XCircle className="mt-0.5 h-4 w-4 flex-shrink-0 text-sev-critical" />
          <p className="min-w-0 flex-1 break-all text-[12.5px] text-sev-critical">{actionError}</p>
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

      {/* 测试结果回显条：紧贴工具条上方，占位稳定不跳动。 */}
      {test.kind !== 'idle' && (
        <div className="flex-shrink-0 border-t border-border px-6 pt-3">
          <TestResultLine test={test} />
        </div>
      )}

      <div className="flex flex-shrink-0 items-center gap-2.5 border-t border-border px-6 py-4">
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
        <button
          type="button"
          onClick={onDeleteClick}
          disabled={saving}
          className={
            confirmingDelete
              ? 'rounded-lg bg-sev-critical px-3.5 py-1.5 text-[13px] text-white transition-colors hover:opacity-90 disabled:cursor-default disabled:opacity-40'
              : 'rounded-lg border border-sev-critical/40 px-4 py-1.5 text-[13px] text-sev-critical transition-colors hover:bg-sev-critical/10 disabled:cursor-default disabled:opacity-40'
          }
        >
          {confirmingDelete ? '确认删除？' : '删除'}
        </button>
        {dirty && (
          <button
            type="button"
            onClick={onReset}
            disabled={saving}
            title="放弃草稿，恢复到打开时的值"
            className="flex items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-[12.5px] text-muted transition-colors hover:border-accent hover:text-accent disabled:cursor-default disabled:opacity-40"
          >
            <RotateCcw className="h-3.5 w-3.5" />
            重置
          </button>
        )}
        <button
          type="button"
          onClick={onSave}
          disabled={saving || saveDisabled || !dirty}
          className="ml-auto rounded-lg bg-accent-soft px-5 py-1.5 text-[13px] text-accent transition-colors hover:bg-accent/10 hover:text-accent-hover disabled:cursor-default disabled:opacity-40"
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
