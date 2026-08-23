import { useMemo, useState, type ReactNode } from 'react'
import { Eye, EyeOff, KeyRound, Loader2, RefreshCw } from 'lucide-react'
import type { ProviderConfig, ProviderType } from '@/api/types'
import { listProviderModels } from '@/api/models'
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
}: {
  draft: ProviderConfig
  patch: (p: Partial<ProviderConfig>) => void
  isNew: boolean
}) {
  const [reveal, setReveal] = useState(false)
  // 编辑态且已有密钥时，默认收起输入（只展示脱敏尾号）；点「更换密钥」才展开。
  const [rotating, setRotating] = useState(false)

  const editingHasKey = !isNew && draft.key_present
  // 收起态：只读展示已配置尾号 +「更换密钥」入口。
  // key_last4 有值 → 展示脱敏尾号「···· 9f2c」；为空（旧行 / 仅 ENV 变量回退，无明文尾号可脱敏）
  // → 展示「来自环境变量」而非误导性的 ····****（那串星号会被当成尾号，实则无尾号可显）。
  if (editingHasKey && !rotating) {
    return (
      <Field label="API Key" hint="密钥加密落库，永不回显；如需替换请点「更换密钥」">
        <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-surface-2 px-3 py-2">
          <span className="flex items-center gap-2 text-[13px] text-text">
            <KeyRound className="h-3.5 w-3.5 text-accent" />
            已配置
            {draft.key_last4 ? (
              <code className="font-mono text-muted">···· {draft.key_last4}</code>
            ) : (
              <span className="text-[12px] text-faint">来自环境变量</span>
            )}
          </span>
          <button
            type="button"
            onClick={() => {
              setRotating(true)
              setReveal(false)
            }}
            className="flex items-center gap-1.5 rounded-md border border-border px-2.5 py-1 text-[12px] text-muted transition-colors hover:border-accent hover:text-accent"
          >
            <RefreshCw className="h-3 w-3" />
            更换密钥
          </button>
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
          className="mt-1.5 text-[12px] text-faint transition-colors hover:text-muted"
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
        key: draft.key || undefined,
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
          className="flex flex-shrink-0 items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-[12.5px] text-muted transition-colors hover:border-accent hover:text-accent disabled:opacity-50"
        >
          {probing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
          探测
        </button>
      </div>
      {probeMsg && <p className="mt-1 text-[12px] text-faint">{probeMsg}</p>}
    </Field>
  )
}

// 新建 provider 空白初值：type 默认 openai_compat（多数国产/开源模型走此协议），
// 能力标志保守（不支持视觉、支持工具调用），context_window 给常见 64K。
export function blankProvider(): ProviderConfig {
  return {
    key: '',
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

// key/base_url/default_model 必填，context_window 必须 > 0（与后端校验对齐）。
// api_key：新建必填（后端 400 拒绝空密钥新建）；编辑留空 = 不修改已存密钥，故不校验。
export function providerInvalid(d: ProviderConfig | null, isNew: boolean): boolean {
  return (
    !d?.key ||
    !d?.base_url ||
    !d?.default_model ||
    (isNew && !d?.api_key) ||
    !(d?.context_window && d.context_window > 0)
  )
}

// 分组小标题：单页表单内用轻量标题分「连接 / 能力」两组，替代原二级 Radix Tabs——
// provider 字段总量不大，一屏滚动比「点 tab 找字段」更快，也避免 tab 内部态与重挂载的耦合。
function GroupLabel({ children }: { children: ReactNode }) {
  return (
    <h3 className="mt-1 text-[11px] font-semibold uppercase tracking-wider text-faint">
      {children}
    </h3>
  )
}

// ProviderFormBody 是 provider 详情表单主体：单页分组（连接 / 能力），无二级 tab。
// key 创建后不可改（角色路由 FK 指向它）。
export function ProviderFormBody({
  draft,
  patch,
  isNew,
}: {
  draft: ProviderConfig
  patch: (p: Partial<ProviderConfig>) => void
  isNew: boolean
}) {
  return (
    <div className="flex flex-col gap-4">
      <GroupLabel>连接</GroupLabel>
      <div className="flex gap-3">
        <Field label="标识键" hint={isNew ? '稳定引用键，创建后不可改' : 'key'}>
          <input
            className={INPUT_CLASS}
            value={draft.key}
            spellCheck={false}
            disabled={!isNew}
            placeholder="deepseek"
            onChange={(e) => patch({ key: e.target.value })}
          />
        </Field>
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
      <ApiKeyField draft={draft} patch={patch} isNew={isNew} />
      <label className="flex items-center gap-2 text-[13px] text-text">
        <input
          type="checkbox"
          checked={draft.enabled}
          onChange={(e) => patch({ enabled: e.target.checked })}
        />
        启用
      </label>

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
      {/* 工具/视觉手动勾选：模型名看不出模态（如小米 MiMo 全模态但名字无 vl 标记），
          无法靠名字前缀可靠推断，故必须让用户按 provider 实际能力显式声明。
          视觉标志错配会导致含图消息路由到不支持读图的 model。 */}
      <label className="flex items-center gap-2 text-[13px] text-text">
        <input
          type="checkbox"
          checked={draft.supports_tools}
          onChange={(e) => patch({ supports_tools: e.target.checked })}
        />
        支持工具调用（function calling）
      </label>
      <label className="flex items-center gap-2 text-[13px] text-text">
        <input
          type="checkbox"
          checked={draft.supports_vision}
          onChange={(e) => patch({ supports_vision: e.target.checked })}
        />
        支持视觉（含图消息路由依赖此标志，须按 provider 实际能力显式声明）
      </label>
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

