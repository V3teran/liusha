import { useEffect, useId, useState, type ReactNode } from 'react'
import { Eye, EyeOff, KeyRound, RefreshCw } from 'lucide-react'
import type { AuthScheme, OutputLimitParam, ProviderConfig, ProviderType } from '@/api/types'
import { Combobox } from '@/components/ui/Combobox'
import { Switch } from '@/components/ui/Switch'
import { Field } from '@/features/config/ConfigDrawer'
import { FIELD_LABEL_CLASS, INPUT_CLASS, NUMBER_INPUT_CLASS } from '@/components/ui/control'
import { Select } from '@/components/ui/Select'
import { cn } from '@/lib/utils'
import {
  baseUrlError,
  chatRequestUrl,
  defaultAuthScheme,
  normalizeBaseUrl,
  thinkingBudgetError,
} from './providerRules'
import { Button } from '@/components/ui/Button'

// 协议类型展示标签（后端枚举值不变，仅前端呈现）。
export const TYPE_LABEL: Record<ProviderType, string> = {
  openai_compat: 'OpenAI 兼容',
  anthropic: 'Anthropic',
}

// 协议说明：只讲协议约定（报文格式、Base URL 写法），不列举任何具体服务。
const PROTOCOLS: { value: ProviderType; title: string; desc: string; placeholder: string }[] = [
  {
    value: 'openai_compat',
    title: 'OpenAI 兼容',
    desc: 'Chat Completions 协议；Base URL 含版本段（如 …/v1）',
    placeholder: 'https://api.example.com/v1',
  },
  {
    value: 'anthropic',
    title: 'Anthropic',
    desc: 'Messages 协议；Base URL 不含 /v1',
    placeholder: 'https://api.example.com/anthropic',
  },
]

const AUTH_SCHEME_LABEL: Record<AuthScheme, string> = {
  bearer: 'Authorization: Bearer',
  'x-api-key': 'x-api-key 头',
}

// 协议选择：两种协议并列单选。新建时不预设——协议决定报文格式与请求路径，选错是接入失败
// 的头号原因，必须由用户明确选择；切换协议时鉴权方式回到该协议的标准方式（运行诊断可改写）。
function ProtocolPicker({
  value,
  onChange,
}: {
  value: ProviderType | ''
  onChange: (t: ProviderType) => void
}) {
  // 单选组不能包进 <label>（label 只关联一个控件，按钮在 label 内还会转发点击）：
  // 独立分组，以 aria-labelledby 命名。
  return (
    <div className="flex flex-col gap-1.5">
      <span id="provider-protocol-label" className={FIELD_LABEL_CLASS}>
        协议
        {!value && <span className="ml-2 font-normal text-text-tertiary">先选择该服务使用的接口协议</span>}
      </span>
      <div role="radiogroup" aria-labelledby="provider-protocol-label" className="grid grid-cols-2 gap-2">
        {PROTOCOLS.map((p) => {
          const checked = value === p.value
          return (
            <button
              key={p.value}
              type="button"
              role="radio"
              aria-checked={checked}
              onClick={() => onChange(p.value)}
              className={cn(
                'flex items-start gap-2.5 rounded-lg border px-3 py-2.5 text-left transition-colors',
                checked
                  ? 'border-accent bg-accent-soft ring-1 ring-accent'
                  : 'border-border bg-surface hover:border-border-strong',
              )}
            >
              {/* 单选指示器：选中态不只靠底色（与 ToggleChip 同一原则） */}
              <span
                aria-hidden
                className={cn(
                  'mt-0.5 flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-full border',
                  checked ? 'border-accent' : 'border-border-strong',
                )}
              >
                {checked && <span className="h-2 w-2 rounded-full bg-accent" />}
              </span>
              <span className="min-w-0">
                <span className="block text-sm font-medium text-text">{p.title}</span>
                <span className={cn('mt-0.5 block text-xs leading-snug', checked ? 'text-text-secondary' : 'text-text-tertiary')}>{p.desc}</span>
              </span>
            </button>
          )
        })}
      </div>
    </div>
  )
}

// Base URL 输入 + 实际请求预览：按协议规则归一后展示运行期真正请求的地址——用户粘贴
// 完整端点或多带了 /v1 时，这里直接显示归一后的结果，所见即所发。
function BaseUrlField({
  draft,
  patch,
}: {
  draft: ProviderConfig
  patch: (p: Partial<ProviderConfig>) => void
}) {
  const protocol = draft.type as ProviderType
  const error = baseUrlError(draft.base_url)
  const preview =
    draft.base_url.trim() && !error ? chatRequestUrl(protocol, normalizeBaseUrl(protocol, draft.base_url)) : ''
  return (
    <Field label="Base URL" hint="服务地址；粘贴完整端点也可，保存时按协议自动归一">
      <input
        className={INPUT_CLASS}
        value={draft.base_url}
        spellCheck={false}
        placeholder={PROTOCOLS.find((p) => p.value === protocol)?.placeholder}
        onChange={(e) => patch({ base_url: e.target.value })}
      />
      {error ? (
        <p role="alert" className="mt-1 text-xs text-sev-critical">
          {error}
        </p>
      ) : (
        preview && (
          <p className="mt-1 break-all font-mono text-[11px] text-text-tertiary">
            实际请求：POST {preview}
          </p>
        )
      )}
    </Field>
  )
}

// 鉴权方式：密钥的携带方式（与协议正交）。默认协议标准方式；运行诊断实测识别后自动回填，
// 中转网关只认另一种时也可手动指定。
function AuthSchemeField({
  value,
  onChange,
}: {
  value: AuthScheme
  onChange: (v: AuthScheme) => void
}) {
  return (
    <Field label="鉴权方式" hint="运行诊断会自动识别；一般无需手改">
      <Select className="w-full" value={value} onChange={(e) => onChange(e.target.value as AuthScheme)}>
        {(Object.keys(AUTH_SCHEME_LABEL) as AuthScheme[]).map((s) => (
          <option key={s} value={s}>
            {AUTH_SCHEME_LABEL[s]}
          </option>
        ))}
      </Select>
    </Field>
  )
}

// 默认模型：手填，或从运行诊断拉到的模型列表中选（可编辑下拉，展开即列出全部）。端点
// 不提供模型列表时只能手填——这不是错误，很多兼容端点只实现对话接口。
// 不用 Field：它以 <label> 包住控件，展开按钮嵌在 label 里会破坏点击与无障碍语义。
function ModelField({
  draft,
  patch,
  models,
}: {
  draft: ProviderConfig
  patch: (p: Partial<ProviderConfig>) => void
  models: string[]
}) {
  const inputId = useId()
  const hint = models.length
    ? `诊断拉到 ${models.length} 个模型，点右侧箭头查看全部，也可直接填写`
    : '填写服务方的模型名；留空运行诊断时，自动选用拉到的模型列表第一个并回填'
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={inputId} className={FIELD_LABEL_CLASS}>
        默认模型
        <span className="ml-2 font-normal text-text-tertiary">{hint}</span>
      </label>
      <Combobox
        id={inputId}
        value={draft.default_model}
        options={models}
        onChange={(v) => patch({ default_model: v })}
        placeholder="model-name"
        toggleLabel="展开模型列表"
      />
    </div>
  )
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
              <span className="min-w-0 truncate text-xs text-text-tertiary">
                来自环境变量{draft.api_key_env ? `（${draft.api_key_env}）` : ''}
              </span>
            ) : (
              <code className="font-mono text-text-secondary">···· {draft.key_last4}</code>
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
                    : 'rounded-md border border-border px-2.5 py-1 text-xs text-text-secondary transition-colors hover:border-accent hover:text-accent'
                }
              >
                {confirmingRestore ? '确认恢复？' : '恢复使用环境变量'}
              </button>
            )}
            <Button
              variant="secondary"
              size="xs"
              onClick={() => {
                setRotating(true)
                setReveal(false)
              }}
            >
              <RefreshCw />
              更换密钥
            </Button>
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
          className="absolute right-2.5 top-1/2 -translate-y-1/2 text-text-tertiary transition-colors hover:text-text-secondary"
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
          className="mt-1.5 text-xs text-text-tertiary transition-colors hover:text-text-secondary"
        >
          取消更换（保留原密钥）
        </button>
      )}
    </Field>
  )
}


// NumberField 是数字字段的统一形态：编辑期间以本地字符串为显示事实源——
// 清空保持空、前导 0 忠实保留（DOM 显示 = 用户键入），数值实时回写 draft。
// 此前 onChange 直接 Number(e.target.value)：Number("")=0 把清空变成「填 0」，
// 受控回写让字段卡着删不掉的 0、前导 0 被悄悄吞掉或累积——用户看到「输入异常」。
// 外部 draft 变化（重置/保存后 commit）时本地串跟随外部值。
function NumberField({
  label,
  hint,
  value,
  min,
  error,
  onValue,
}: {
  label: string
  hint?: string
  value: number | '' | undefined
  min?: number
  error?: string // 非空时以警示色就地显示（保存为何被禁，就近可读）
  onValue: (v: number | '') => void
}) {
  const [text, setText] = useState(value === '' || value == null ? '' : String(value))
  // 外部值变化（重置 / 保存后 commit 的行数据）→ 本地串跟随；用户正在编辑的
  // 串若与外部数值一致则不打扰（避免每次按键后的回写把前导 0 抹掉）。
  useEffect(() => {
    const external = value === '' || value == null ? '' : String(value)
    setText((cur) => (Number(cur) === Number(external) || cur === external ? cur : external))
  }, [value])
  return (
    <Field label={label} hint={hint}>
      <input
        type="number"
        min={min}
        className={NUMBER_INPUT_CLASS}
        value={text}
        onChange={(e) => {
          const t = e.target.value
          setText(t)
          onValue(t === '' ? '' : Number(t))
        }}
      />
      {error && (
        <p role="alert" className="mt-1 text-xs text-sev-critical">
          {error}
        </p>
      )}
    </Field>
  )
}


// 分组小标题：单页表单内用轻量标题分「连接 / 能力」两组，替代原二级 Radix Tabs——
// provider 字段总量不大，一屏滚动比「点 tab 找字段」更快，也避免 tab 内部态与重挂载的耦合。
function GroupLabel({ children }: { children: ReactNode }) {
  return (
    <h3 className="mt-1 text-xs font-semibold uppercase tracking-wider text-text-tertiary">
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
        {hint && <span className="mt-0.5 block text-xs leading-snug text-text-tertiary">{hint}</span>}
      </span>
      <Switch checked={checked} onCheckedChange={onCheckedChange} aria-label={label} />
    </div>
  )
}


// ProviderFormBody 是 provider 详情表单主体：先选协议，再填连接与能力（单页分组，无二级 tab）。
// 协议未选时只展示协议选择——后续字段的写法（Base URL 约定、鉴权方式、思考预算）都取决于它。
// models 是运行诊断拉到的模型列表（供默认模型下拉）；diagnosis 是连接诊断面板，紧跟在连接
// 参数（地址、密钥、鉴权方式、模型）之后——填完即可就地诊断，回填的字段也都在近旁。
export function ProviderFormBody({
  draft,
  patch,
  isNew,
  onRestoreEnvKey,
  models = [],
  diagnosis,
}: {
  draft: ProviderConfig
  patch: (p: Partial<ProviderConfig>) => void
  isNew: boolean
  onRestoreEnvKey?: () => void
  models?: string[]
  diagnosis?: ReactNode
}) {
  const onProtocol = (t: ProviderType) => {
    if (t !== draft.type) patch({ type: t, auth_scheme: defaultAuthScheme(t) })
  }
  return (
    <div className="flex flex-col gap-4">
      <GroupLabel>连接</GroupLabel>
      <ProtocolPicker value={draft.type} onChange={onProtocol} />
      {draft.type && (
        <>
          <Field label="显示名称" hint="必填；列表与能力路由按它显示，不可重名，可随时改">
            <input
              className={INPUT_CLASS}
              value={draft.display_name}
              spellCheck={false}
              placeholder="主力部署"
              onChange={(e) => patch({ display_name: e.target.value })}
            />
          </Field>
          <BaseUrlField draft={draft} patch={patch} />
          <ApiKeyField
            draft={draft}
            patch={patch}
            isNew={isNew}
            onRestoreEnvKey={onRestoreEnvKey}
          />
          <AuthSchemeField value={draft.auth_scheme} onChange={(v) => patch({ auth_scheme: v })} />
          <ModelField draft={draft} patch={patch} models={models} />
          {diagnosis}
          <ToggleField
            label="启用"
            hint="停用后运行期跳过该部署，它所在的档位由相邻档承接；配置保留"
            checked={draft.enabled}
            onCheckedChange={(v) => patch({ enabled: v })}
          />

          <GroupLabel>能力</GroupLabel>
          <NumberField
            label="上下文窗口"
            hint="model 总上下文 tokens；ReAct 历史压缩按此算阈值，必填 > 0"
            value={draft.context_window}
            min={1}
            error={
              typeof draft.context_window !== 'number' || draft.context_window <= 0
                ? '必填且需大于 0——为空或 0 时保存不可用'
                : undefined
            }
            onValue={(v) => patch({ context_window: v })}
          />
          <NumberField
            label="单次最大输出 tokens"
            hint="0 表示按协议默认（4096）"
            value={draft.max_tokens}
            min={0}
            onValue={(v) => patch({ max_tokens: v })}
          />
          {draft.type === 'openai_compat' && (
            <Field label="输出上限字段" hint="多数服务用 max_tokens，部分推理模型只认 max_completion_tokens；运行诊断与运行期都会自动识别">
              <Select
                className="w-full"
                value={draft.output_limit_param}
                onChange={(e) => patch({ output_limit_param: e.target.value as OutputLimitParam })}
              >
                <option value="max_tokens">max_tokens</option>
                <option value="max_completion_tokens">max_completion_tokens</option>
              </Select>
            </Field>
          )}
          {draft.type === 'anthropic' && (
            <NumberField
              label="扩展思考预算"
              hint="thinking budget tokens：0 关闭；开启时至少 1024，且小于单次最大输出 tokens"
              value={draft.thinking_budget_tokens}
              min={0}
              error={thinkingBudgetError(draft)}
              onValue={(v) => patch({ thinking_budget_tokens: v })}
            />
          )}
          {/* 工具/视觉能力：工具调用由运行诊断实测后自动勾选；视觉无法低成本实测，按服务实际能力声明。 */}
          <ToggleField
            label="支持工具调用"
            hint="运行诊断会实测并自动设置；四个智能体都依赖工具调用，未支持的部署承接智能体所在档位时能力路由会告警"
            checked={draft.supports_tools}
            onCheckedChange={(v) => patch({ supports_tools: v })}
          />
          <ToggleField
            label="支持视觉"
            hint="含图消息的路由依赖此标志，须按服务实际能力声明"
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
        </>
      )}
    </div>
  )
}
