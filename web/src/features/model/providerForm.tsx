import { useEffect, useId, useState, type ReactNode } from 'react'
import { Eye, EyeOff, KeyRound, RefreshCw } from 'lucide-react'
import type { AuthScheme, OutputLimitParam, ProviderConfig, ProviderType, ReasoningEffort } from '@/api/types'
import { Switch } from '@/components/ui/Switch'
import { Field } from '@/features/config/ConfigDrawer'
import { FIELD_LABEL_CLASS, INPUT_CLASS, NUMBER_INPUT_CLASS } from '@/components/ui/control'
import { Select } from '@/components/ui/Select'
import { cn } from '@/lib/utils'
import {
  baseUrlError,
  chatRequestUrl,
  DEFAULT_CONTEXT_WINDOW,
  defaultAuthScheme,
  DEFAULT_MAX_TOKENS,
  normalizeBaseUrl,
  REASONING_EFFORT_OPTIONS,
} from './providerRules'
import { Button } from '@/components/ui/Button'
import { ModelField } from './ModelField'

// 协议的展示名：厂商 + 接口名，与后端取值一一对应——同一家有多种协议时只写厂商分不清。
export const TYPE_LABEL: Record<ProviderType, string> = {
  openai_chat_completions: 'OpenAI Chat Completions',
  openai_responses: 'OpenAI Responses',
  anthropic_messages: 'Anthropic Messages',
}

// 协议选项：只列协议，不列举任何具体服务；Base URL 的写法由占位示例与「实际请求」预览表达。
// title 取厂商、desc 取接口名：同一家有几个协议时它们在选单里挨在一起，看得出是同一家的不同接口。
const PROTOCOLS: { value: ProviderType; title: string; desc: string; placeholder: string }[] = [
  {
    value: 'openai_chat_completions',
    title: 'OpenAI',
    desc: 'Chat Completions',
    placeholder: 'https://api.example.com/v1',
  },
  {
    value: 'openai_responses',
    title: 'OpenAI',
    desc: 'Responses',
    placeholder: 'https://api.example.com/v1',
  },
  {
    value: 'anthropic_messages',
    title: 'Anthropic',
    desc: 'Messages',
    placeholder: 'https://api.example.com/anthropic',
  },
]

const AUTH_SCHEME_LABEL: Record<AuthScheme, string> = {
  bearer: 'Authorization: Bearer',
  'x-api-key': 'x-api-key 头',
}

// 协议选择：三种协议并列单选。新建时不预设——协议决定报文格式与请求路径，选错是接入失败
// 的头号原因，必须由用户明确选择；切换协议时鉴权方式回到该协议的标准方式（测试连接可改写）。
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
      </span>
      <div role="radiogroup" aria-labelledby="provider-protocol-label" className="grid gap-2 sm:grid-cols-3">
        {PROTOCOLS.map((p) => {
          const checked = value === p.value
          return (
            <button
              key={p.value}
              type="button"
              role="radio"
              aria-checked={checked}
              aria-label={TYPE_LABEL[p.value]}
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
    <Field label="Base URL">
      <input
        className={INPUT_CLASS}
        value={draft.base_url}
        required
        spellCheck={false}
        placeholder={PROTOCOLS.find((p) => p.value === protocol)?.placeholder}
        onChange={(e) => patch({ base_url: e.target.value })}
      />
      {error ? (
        <p role="alert" className="mt-1 text-xs text-status-error">
          {error}
        </p>
      ) : (
        preview && (
          <p className="mt-1 break-all font-mono text-2xs text-text-tertiary">
            实际请求：POST {preview}
          </p>
        )
      )}
    </Field>
  )
}

// 鉴权方式：密钥的携带方式（与协议正交）。默认协议标准方式；测试连接实测识别后自动回填，
// 中转网关只认另一种时也可手动指定。
function AuthSchemeField({
  value,
  onChange,
}: {
  value: AuthScheme
  onChange: (v: AuthScheme) => void
}) {
  return (
    <Field label="鉴权方式">
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
  const labelId = useId()
  // 编辑态且已有密钥时，默认收起输入（只展示脱敏尾号）；点「更换密钥」才展开。
  const [rotating, setRotating] = useState(false)

  const editingHasKey = !isNew && draft.key_present
  // 收起态：只读展示脱敏尾号「···· 9f2c」+「更换密钥」入口。
  if (editingHasKey && !rotating) {
    return (
      // 收起态没有输入控件：不用 Field（<label> 会把唯一的按钮当作被标注控件、改写它的可访问名），
      // 以分组 + aria-labelledby 命名。
      <div role="group" aria-labelledby={labelId} className="flex flex-col gap-1.5">
        <span id={labelId} className={FIELD_LABEL_CLASS}>
          API Key
        </span>
        <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-surface-2 px-3 py-2">
          <span className="flex min-w-0 items-center gap-2 text-sm text-text">
            <KeyRound className="h-3.5 w-3.5 flex-shrink-0 text-accent" />
            已配置
            <code className="font-mono text-text-secondary">···· {draft.key_last4}</code>
          </span>
          <div className="flex flex-shrink-0 items-center gap-1.5">
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
      </div>
    )
  }

  // 编辑态且没有密钥：该 provider 的调用必然失败，就地告警促其补齐。
  const missing = !isNew && !rotating && !draft.api_key
  return (
    <Field label="API Key">
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
          // 点击区 32px 宽（图标只有 14px）：WCAG 2.2 要求点击目标至少 24×24
          className="absolute inset-y-0 right-0 flex w-8 items-center justify-center text-text-tertiary transition-colors hover:text-text-secondary"
        >
          {reveal ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
        </button>
      </div>
      {missing && (
        <p role="alert" className="mt-1 text-xs text-status-error">
          尚无密钥，调用会失败
        </p>
      )}
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
          取消更换
        </button>
      )}
    </Field>
  )
}


// NumberField 是数字字段的统一形态：编辑期间以本地字符串为显示事实源——
// 清空保持空、前导 0 忠实保留（DOM 显示 = 用户键入），数值实时回写 draft。
// 此前 onChange 直接 Number(e.target.value)：Number("")=0 把清空变成「填 0」，
// 受控回写让字段卡着删不掉的 0、前导 0 被悄悄吞掉或累积——用户看到「输入异常」。
// 外部 draft 变化（重置、服务端回填）时本地串跟随外部值。
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
  // 外部值变化（重置 / 服务端回填的行数据）→ 本地串跟随；用户正在编辑的
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
        <p role="alert" className="mt-1 text-xs text-status-error">
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


// 布尔设置行：标题在左、开关在右的两端对齐行（设置页标准布局）。
// 替代原生 checkbox——开关语义（启用/能力声明）与全站控件体系一致，热区也更大。
function ToggleField({
  label,
  checked,
  onCheckedChange,
}: {
  label: string
  checked: boolean
  onCheckedChange: (v: boolean) => void
}) {
  return (
    <div className="flex items-center justify-between gap-4 rounded-lg border border-border bg-surface-2/50 px-4 py-2.5">
      <span className="min-w-0 text-sm text-text">{label}</span>
      <Switch checked={checked} onCheckedChange={onCheckedChange} aria-label={label} />
    </div>
  )
}


// ProviderFormBody 是 provider 详情表单主体：先选协议，再填连接与能力（单页分组，无二级 tab）。
// 协议未选时只展示协议选择——后续字段的写法（Base URL 约定、鉴权方式、输出上限字段）都取决于它。
// diagnosis 是测试连接面板，紧跟在连接参数（地址、密钥、鉴权方式、模型）之后——填完即可
// 就地测试，回填的字段也都在近旁。
export function ProviderFormBody({
  draft,
  patch,
  isNew,
  diagnosis,
}: {
  draft: ProviderConfig
  patch: (p: Partial<ProviderConfig>) => void
  isNew: boolean
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
          <Field label="显示名称">
            <input
              className={INPUT_CLASS}
              value={draft.display_name}
              required
              spellCheck={false}
              placeholder="主力模型"
              onChange={(e) => patch({ display_name: e.target.value })}
            />
          </Field>
          <BaseUrlField draft={draft} patch={patch} />
          <ApiKeyField draft={draft} patch={patch} isNew={isNew} />
          <AuthSchemeField value={draft.auth_scheme} onChange={(v) => patch({ auth_scheme: v })} />
          <ModelField draft={draft} patch={patch} />
          {diagnosis}
          <ToggleField
            label="启用"
            checked={draft.enabled}
            onCheckedChange={(v) => patch({ enabled: v })}
          />

          <GroupLabel>能力</GroupLabel>
          <NumberField
            label="上下文窗口"
            hint={`缺省 ${DEFAULT_CONTEXT_WINDOW}；查模型文档：一次对话的总容量（输入+输出），也决定何时压缩历史`}
            value={draft.context_window}
            min={1}
            error={
              typeof draft.context_window !== 'number' || draft.context_window <= 0
                ? '需大于 0'
                : undefined
            }
            onValue={(v) => patch({ context_window: v })}
          />
          <NumberField
            label="单次最大输出 tokens"
            hint={`缺省 ${DEFAULT_MAX_TOKENS}；以模型文档的输出上限为准，高出时被上游拒绝后会自动下调`}
            value={draft.max_tokens}
            min={0}
            onValue={(v) => patch({ max_tokens: v })}
          />
          {draft.type === 'openai_chat_completions' && (
            <Field label="输出上限字段">
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
          <Field label="思考深度" hint="推理模型投入多少推理；按模型默认即不下发这个参数。只对带推理的模型有意义">
            <Select
              className="w-full"
              value={draft.reasoning_effort}
              onChange={(e) => patch({ reasoning_effort: e.target.value as ReasoningEffort | '' })}
            >
              <option value="">按模型默认</option>
              {REASONING_EFFORT_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </Select>
          </Field>
          {/* 工具/视觉能力：工具调用由测试连接实测后自动勾选；视觉无法低成本实测，按服务实际能力声明。 */}
          <ToggleField
            label="支持工具调用"
            checked={draft.supports_tools}
            onCheckedChange={(v) => patch({ supports_tools: v })}
          />
          <ToggleField
            label="支持视觉"
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
