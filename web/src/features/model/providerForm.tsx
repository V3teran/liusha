import { useMemo, useState, type ReactNode } from 'react'
import { Eye, EyeOff, KeyRound, Loader2, RefreshCw } from 'lucide-react'
import type { ProviderConfig, ProviderType } from '@/api/types'
import { listProviderModels } from '@/api/models'
import { Switch } from '@/components/ui/switch'
import { Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'

// 协议类型展示标签（后端枚举值不变，仅前端呈现）。
export const TYPE_LABEL: Record<ProviderType, string> = {
  openai_compat: 'OpenAI 兼容',
  anthropic: 'Anthropic',
}

// API Key 三态输入（写-only 凭据模型，密钥落库后永不回显）：
//   1. 新建：密码框 + 小眼睛，必填。
//   2. 编辑·已有密钥：默认只读展示「已配置 ····{last4}」+「更换密钥」按钮——
//      点开才出输入框（避免误清空已存密钥）；不点 = api_key 留空 = 保留原密钥。
//   3. 编辑·无密钥：红色告警 + 输入框（该 provider 调用必失败，促其补齐）。
function ApiKeyField({
  draft,
  patch,
  isNew,
  onRestoreEnvKey,
}: {
  draft: ProviderConfig
  patch: (p: Partial<ProviderConfig>) => void
  isNew: boolean
  onRestoreEnvKey?: () => void
}) {
  const [reveal, setReveal] = useState(false)
  // 编辑态且已有密钥时，默认收起输入（只展示脱敏尾号）；点「更换密钥」才展开。
  const [rotating, setRotating] = useState(false)

  const editingHasKey = !isNew && draft.key_present
  // 收起态：只读展示已配置尾号 +「更换密钥」入口。
  // key_last4 有值 → 展示脱敏尾号「···· 9f2c」；为空（旧行 / 仅 ENV 变量回退，无明文尾号可脱敏）
  // → 展示「来自环境变量」而非误导性的 ····****（那串星号会被当成尾号，实则无尾号可显）。
  // 恢复使用环境变量：仅当密文生效且存在 env 引用时可用（两段式确认）。
  const [confirmingRestore, setConfirmingRestore] = useState(false)

  if (editingHasKey && !rotating) {
    const fromEnv = !draft.key_last4
    return (
      <Field
        label="API Key"
        hint={
          fromEnv
            ? '密钥来自环境变量（部署默认），未落库；点「更换密钥」设置后以此为准'
            : '密钥加密落库，永不回显；如需替换请点「更换密钥」'
        }
      >
        <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-surface-2 px-3 py-2">
          <span className="flex min-w-0 items-center gap-2 text-sm text-text">
            <KeyRound className="h-3.5 w-3.5 flex-shrink-0 text-accent" />
            已配置
            {fromEnv ? (
              <span className="min-w-0 truncate text-xs text-faint">
                来自环境变量{draft.api_key_env ? `（${draft.api_key_env}）` : ''}
              </span>
            ) : (
              <code className="font-mono text-muted">···· {draft.key_last4}</code>
            )}
          </span>
          <div className="flex flex-shrink-0 items-center gap-1.5">
            {onRestoreEnvKey && !fromEnv && draft.api_key_env && (
              <button
                type="button"
                onClick={() => {
                  if (confirmingRestore) {
                    setConfirmingRestore(false)
                    onRestoreEnvKey()
                    return
                  }
                  setConfirmingRestore(true)
                  window.setTimeout(() => setConfirmingRestore(false), 3000)
                }}
                title={confirmingRestore ? '再点一次确认：清除已存密文，回落环境变量' : `清除已存密文，回落环境变量 ${draft.api_key_env}`}
                className={
                  confirmingRestore
                    ? 'rounded-md bg-sev-critical/10 px-2.5 py-1 text-xs text-sev-critical'
                    : 'rounded-md border border-border px-2.5 py-1 text-xs text-muted transition-colors hover:border-accent hover:text-accent'
                }
              >
                {confirmingRestore ? '确认恢复？' : '恢复使用环境变量'}
              </button>
            )}
            <button
              type="button"
              onClick={() => {
                setRotating(true)
                setReveal(false)
              }}
              className="flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1 text-xs text-muted transition-colors hover:border-accent hover:text-accent"
            >
              <RefreshCw className="h-3 w-3" />
              更换密钥
            </button>
          </div>
        </div>
      </Field>
    )
  }

  const hint = isNew
    ? '明文提交，后端加密后落库，此后不再展示'
    : rotating
      ? '填入新密钥替换已存（留空取消则保留原密钥）'
      : '该 provider 尚无密钥，调用会失败——请填入后保存'
  return (
    <Field label="API Key" hint={hint}>
      <div className="relative">
        <input
          className={INPUT_CLASS + ' pr-9'}
          type={reveal ? 'text' : 'password'}
          value={draft.api_key ?? ''}
          spellCheck={false}
          autoComplete="off"
          placeholder={isNew ? '粘贴密钥…' : rotating ? '粘贴新密钥…' : '尚未设置'}
          onChange={(e) => patch({ api_key: e.target.value })}
        />
        <button
          type="button"
          onClick={() => setReveal((v) => !v)}
          aria-label={reveal ? '隐藏密钥' : '显示密钥'}
          className="absolute right-2.5 top-1/2 -translate-y-1/2 text-faint transition-colors hover:text-muted"
        >
          {reveal ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
        </button>
      </div>
      {rotating && (
        <button
          type="button"
          onClick={() => {
            setRotating(false)
            setReveal(false)
            patch({ api_key: '' })
          }}
          className="mt-1.5 text-xs text-faint transition-colors hover:text-muted"
        >
          取消更换（保留原密钥）
        </button>
      )}
    </Field>
  )
}

// 默认模型输入：可手填 + 从 provider /models 端点探测下拉（datalist）。
// 探测据当前表单值（base_url + 密钥来源）发 POST /models/providers/list-models；
// 拉不到（provider 不支持 /models）静默回退纯手填，不阻塞。
function ModelField({
  draft,
  patch,
}: {
  draft: ProviderConfig
  patch: (p: Partial<ProviderConfig>) => void
}) {
  const [models, setModels] = useState<string[]>([])
  const [probing, setProbing] = useState(false)
  const [probeMsg, setProbeMsg] = useState('')
  const listId = useMemo(() => `models-${Math.random().toString(36).slice(2, 8)}`, [])

  const probe = async () => {
    if (!draft.base_url) {
      setProbeMsg('请先填 Base URL')
      return
    }
    setProbing(true)
    setProbeMsg('')
    try {
      const list = await listProviderModels({
        code: draft.code || undefined,
        type: draft.type,
        base_url: draft.base_url,
        api_key: draft.api_key || undefined,
      })
      setModels(list)
      setProbeMsg(list.length ? `探测到 ${list.length} 个模型` : '该 provider 未返回模型列表，请手填')
    } catch (e) {
      setProbeMsg(e instanceof Error ? e.message : '探测失败，请手填')
    } finally {
      setProbing(false)
    }
  }

  const onModel = (model: string) => patch({ default_model: model })

  return (
    <Field label="默认模型" hint="该部署的默认 model 名；可点「探测」从 provider 拉取可选列表">
      <div className="flex gap-2">
        <input
          className={INPUT_CLASS}
          list={listId}
          value={draft.default_model}
          spellCheck={false}
          placeholder="deepseek-chat"
          onChange={(e) => onModel(e.target.value)}
        />
        <datalist id={listId}>
          {models.map((m) => (
            <option key={m} value={m} />
          ))}
        </datalist>
        <button
          type="button"
          onClick={() => void probe()}
          disabled={probing}
          className="flex flex-shrink-0 items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-sm text-muted transition-colors hover:border-accent hover:text-accent disabled:opacity-50"
        >
          {probing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
          探测
        </button>
      </div>
      {probeMsg && <p className="mt-1 text-xs text-faint">{probeMsg}</p>}
    </Field>
  )
}

// 新建 provider 空白初值：type 默认 openai_compat（多数国产/开源模型走此协议），
// 能力标志保守（不支持视觉、支持工具调用），context_window 给常见 64K。
// 显示名兜底：昵称为空时回退 code——全站展示统一走这里。
export function providerDisplayName(p: Pick<ProviderConfig, 'code' | 'display_name'>): string {
  return p.display_name?.trim() || p.code
}

export function blankProvider(): ProviderConfig {
  return {
    code: '',
    display_name: '',
    type: 'openai_compat',
    base_url: '',
    default_model: '',
    api_key: '',
    key_present: false,
    max_tokens: 4096,
    supports_tools: true,
    supports_vision: false,
    context_window: 65536,
    description: '',
    sort_order: 0,
    enabled: true,
  }
}

// base_url/default_model 必填，context_window 必须 > 0（与后端校验对齐）。
// 标识符不是用户职责：POST 由后端生成 UUID，编辑态 code 来自既有行。
// api_key：新建必填（后端 400 拒绝空密钥新建）；编辑留空 = 不修改已存密钥，故不校验。
export function providerInvalid(d: ProviderConfig | null, _isNew: boolean): boolean {
  return (
    !d?.base_url ||
    !d?.default_model ||
    (_isNew && !d?.api_key) ||
    !(d?.context_window && d.context_window > 0)
  )
}

// 分组小标题：单页表单内用轻量标题分「连接 / 能力」两组，替代原二级 Radix Tabs——
// provider 字段总量不大，一屏滚动比「点 tab 找字段」更快，也避免 tab 内部态与重挂载的耦合。
function GroupLabel({ children }: { children: ReactNode }) {
  return (
    <h3 className="mt-1 text-xs font-semibold uppercase tracking-wider text-faint">
      {children}
    </h3>
  )
}

// 布尔设置行：标题 + 说明在左、开关在右的两端对齐行（设置页标准布局）。
// 替代原生 checkbox——开关语义（启用/能力声明）与全站控件体系一致，热区也更大。
function ToggleField({
  label,
  hint,
  checked,
  onCheckedChange,
}: {
  label: string
  hint?: string
  checked: boolean
  onCheckedChange: (v: boolean) => void
}) {
  return (
    <div className="flex items-center justify-between gap-4 rounded-lg border border-border bg-surface-2/50 px-4 py-2.5">
      <span className="min-w-0">
        <span className="block text-sm text-text">{label}</span>
        {hint && <span className="mt-0.5 block text-xs leading-snug text-faint">{hint}</span>}
      </span>
      <Switch checked={checked} onCheckedChange={onCheckedChange} aria-label={label} />
    </div>
  )
}

// ProviderFormBody 是 provider 详情表单主体：单页分组（连接 / 能力），无二级 tab。
// key 创建后不可改（角色路由 FK 指向它）。
export function ProviderFormBody({
  draft,
  patch,
  isNew,
  onRestoreEnvKey,
}: {
  draft: ProviderConfig
  patch: (p: Partial<ProviderConfig>) => void
  isNew: boolean
  onRestoreEnvKey?: () => void
}) {
  return (
    <div className="flex flex-col gap-4">
      <GroupLabel>连接</GroupLabel>
      <Field label="显示名称" hint="仅展示用，可随时改；留空则显示标识符">
        <input
          className={INPUT_CLASS}
          value={draft.display_name ?? ''}
          spellCheck={false}
          placeholder="glm 主力部署"
          onChange={(e) => patch({ display_name: e.target.value })}
        />
      </Field>
      <div className="flex gap-3">
        {isNew ? (
          <Field label="标识符" hint="保存后由系统生成 UUID，无需填写">
            <div className="flex h-[34px] items-center rounded-md border border-border bg-surface-2 px-2.5 font-mono text-xs text-faint">
              保存后自动生成
            </div>
          </Field>
        ) : (
          <Field label="标识符" hint="稳定引用键，创建后不可改">
            <input className={INPUT_CLASS} value={draft.code} disabled />
          </Field>
        )}
        <Field label="协议">
          <select
            className={INPUT_CLASS}
            value={draft.type}
            onChange={(e) => patch({ type: e.target.value as ProviderType })}
          >
            <option value="openai_compat">OpenAI 兼容</option>
            <option value="anthropic">Anthropic</option>
          </select>
        </Field>
      </div>
      <Field label="Base URL" hint="API 端点根地址">
        <input
          className={INPUT_CLASS}
          value={draft.base_url}
          spellCheck={false}
          placeholder="https://api.deepseek.com"
          onChange={(e) => patch({ base_url: e.target.value })}
        />
      </Field>
      <ModelField draft={draft} patch={patch} />
      <ApiKeyField draft={draft} patch={patch} isNew={isNew} onRestoreEnvKey={onRestoreEnvKey} />
      <ToggleField
        label="启用"
        hint="停用后该部署不再参与路由解析，但配置保留"
        checked={draft.enabled}
        onCheckedChange={(v) => patch({ enabled: v })}
      />

      <GroupLabel>能力</GroupLabel>
      <Field label="上下文窗口" hint="model 总上下文 tokens；ReAct 历史压缩按此算阈值，必填 > 0">
        <input
          type="number"
          min={1}
          className={INPUT_CLASS}
          value={draft.context_window}
          onChange={(e) => patch({ context_window: Number(e.target.value) })}
        />
      </Field>
      <Field label="单次最大输出 tokens" hint="max_tokens，单次生成上限；一般随 provider 默认即可">
        <input
          type="number"
          min={0}
          className={INPUT_CLASS}
          value={draft.max_tokens}
          onChange={(e) => patch({ max_tokens: Number(e.target.value) })}
        />
      </Field>
      {/* 工具/视觉手动声明：模型名看不出模态（如小米 MiMo 全模态但名字无 vl 标记），
          无法靠名字前缀可靠推断，故必须让用户按 provider 实际能力显式声明。
          视觉标志错配会导致含图消息路由到不支持读图的 model。 */}
      <ToggleField
        label="支持工具调用"
        hint="function calling——ReAct 循环依赖工具调用，不勾选则该部署不参与执行类档位"
        checked={draft.supports_tools}
        onCheckedChange={(v) => patch({ supports_tools: v })}
      />
      <ToggleField
        label="支持视觉"
        hint="含图消息的路由依赖此标志，须按 provider 实际能力显式声明"
        checked={draft.supports_vision}
        onCheckedChange={(v) => patch({ supports_vision: v })}
      />
      <Field label="描述">
        <input
          className={INPUT_CLASS}
          value={draft.description}
          onChange={(e) => patch({ description: e.target.value })}
        />
      </Field>
    </div>
  )
}
