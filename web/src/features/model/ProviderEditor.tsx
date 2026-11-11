import { useState } from 'react'
import { AlertTriangle, RotateCcw, X, XCircle } from 'lucide-react'
import type { ProviderConfig } from '@/api/types'
import { Badge } from '@/components/ui/Badge'
import { ProviderFormBody } from './providerForm'
import { DiagnosisPanel } from './DiagnosisPanel'
import { Button } from '@/components/ui/Button'

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
  onCancel: () => void // 取消编辑、回到列表（有未保存更改时先出确认条）
  onDiscard: () => void // 确认条「丢弃」：执行被拦的收起/切换
  onKeepEditing: () => void
  onDismissError: () => void
}

// 行内下拉展开的 provider 编辑面板：单页分组表单（连接 → 连接诊断 → 能力）+ 底部工具条。
// 工具条：重置（dirty 时出现）· 取消 · 保存。面板的空白布局层标记为
// data-editor-surface：点在这些空白处与点在列表外一样视为「点别处」（见 ProvidersView）。
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
  onCancel,
  onDiscard,
  onKeepEditing,
  onDismissError,
}: ProviderEditorProps) {
  // 运行诊断拉到的模型列表（供默认模型下拉）；编辑面板重挂载（切换行）即清空。
  const [models, setModels] = useState<string[]>([])

  return (
    <div data-editor-surface className="border-t border-border-subtle bg-surface-2/30 px-4 py-4">
      <div data-editor-surface className="mb-3 flex items-center justify-between gap-3">
        <h3 className="text-sm font-semibold text-text">
          {isNew ? '新建 provider' : `编辑 ${draft.display_name}`}
        </h3>
        {dirty && (
          <Badge variant="outline" className="border-accent/60 text-accent">
            未保存
          </Badge>
        )}
      </div>
      <div className="max-w-xl">
        <ProviderFormBody
          draft={draft}
          patch={onPatch}
          isNew={isNew}
          onRestoreEnvKey={onRestoreEnvKey}
          models={models}
          diagnosis={<DiagnosisPanel draft={draft} onApply={onPatch} onModels={setModels} />}
        />
      </div>

      {/* dirty 收起确认条：丢弃 / 继续编辑。 */}
      {pendingIntent !== null && (
        <div className="mt-3 flex items-center gap-3 rounded-lg border border-sev-high/40 bg-sev-high/5 px-3.5 py-2.5">
          <AlertTriangle className="h-4 w-4 flex-shrink-0 text-sev-high" />
          <p className="min-w-0 flex-1 text-xs text-text">
            有未保存更改——{pendingIntent === 'switch' ? '切换到其他行' : '收起'}将丢弃这些更改
          </p>
          <Button variant="secondary" size="xs" onClick={onKeepEditing}>
            继续编辑
          </Button>
          <Button variant="danger" size="xs" onClick={onDiscard}>
            {pendingIntent === 'switch' ? '丢弃并切换' : '丢弃并收起'}
          </Button>
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
            className="flex-shrink-0 text-text-tertiary transition-colors hover:text-text"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      )}

      <div data-editor-surface className="mt-4 flex items-center gap-2.5">
        {dirty && (
          <Button variant="secondary" size="sm" onClick={onReset} disabled={saving} title="放弃草稿，恢复到打开时的值">
            <RotateCcw />
            重置
          </Button>
        )}
        <Button variant="secondary" className="ml-auto" onClick={onCancel} disabled={saving}>
          取消
        </Button>
        <Button onClick={onSave} disabled={saving || saveDisabled || !dirty}>
          {saving ? '保存中…' : '保存'}
        </Button>
      </div>
    </div>
  )
}
