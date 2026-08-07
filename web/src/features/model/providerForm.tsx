import type { ProviderConfig, ProviderType } from '@/api/types'
import { Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'

// 协议类型展示标签（后端枚举值不变，仅前端呈现）。
export const TYPE_LABEL: Record<ProviderType, string> = {
  openai_compat: 'OpenAI 兼容',
  anthropic: 'Anthropic',
}

// 新建 provider 空白初值：type 默认 openai_compat（多数国产/开源模型走此协议），
// 能力标志保守（不支持视觉、支持工具调用），context_window 给常见 64K。
export function blankProvider(): ProviderConfig {
  return {
    key: '',
    type: 'openai_compat',
    base_url: '',
    default_model: '',
    api_key_env: '',
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

// key/base_url/default_model/api_key_env 必填，context_window 必须 > 0（与后端校验对齐）。
export function providerInvalid(d: ProviderConfig | null): boolean {
  return (
    !d?.key ||
    !d?.base_url ||
    !d?.default_model ||
    !d?.api_key_env ||
    !(d?.context_window && d.context_window > 0)
  )
}

// 抽屉分页：连接 / 能力两页。key 创建后不可改（角色路由 FK 指向它）。
export function providerTabs(
  draft: ProviderConfig,
  patch: (p: Partial<ProviderConfig>) => void,
  isNew: boolean,
) {
  return [
    {
      value: 'conn',
      label: '连接',
      content: (
        <>
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
          <Field label="默认模型" hint="该部署的默认 model 名">
            <input
              className={INPUT_CLASS}
              value={draft.default_model}
              spellCheck={false}
              placeholder="deepseek-chat"
              onChange={(e) => patch({ default_model: e.target.value })}
            />
          </Field>
          <Field
            label="API Key 环境变量名"
            hint="仅填变量名（如 DEEPSEEK_API_KEY），密钥值存于环境、永不入库"
          >
            <input
              className={INPUT_CLASS}
              value={draft.api_key_env}
              spellCheck={false}
              placeholder="DEEPSEEK_API_KEY"
              onChange={(e) => patch({ api_key_env: e.target.value })}
            />
          </Field>
          {!isNew && (
            <p
              className={
                'rounded-md border px-3 py-2 text-[12.5px] ' +
                (draft.key_present
                  ? 'border-accent/40 bg-accent/10 text-accent'
                  : 'border-sev-high/40 bg-sev-high/10 text-sev-high')
              }
            >
              {draft.key_present
                ? `环境变量 ${draft.api_key_env} 已注入`
                : `环境变量 ${draft.api_key_env} 当前为空——该 provider 调用会失败，请在部署环境注入后重启`}
            </p>
          )}
          <label className="flex items-center gap-2 text-[13px] text-text">
            <input
              type="checkbox"
              checked={draft.enabled}
              onChange={(e) => patch({ enabled: e.target.checked })}
            />
            启用
          </label>
        </>
      ),
    },
    {
      value: 'caps',
      label: '能力',
      content: (
        <>
          <Field label="上下文窗口" hint="model 总上下文 tokens；ReAct 历史压缩按此算阈值，必填 > 0">
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={draft.context_window}
              onChange={(e) => patch({ context_window: Number(e.target.value) })}
            />
          </Field>
          <Field label="单次最大输出 tokens" hint="max_tokens，单次生成上限">
            <input
              type="number"
              min={0}
              className={INPUT_CLASS}
              value={draft.max_tokens}
              onChange={(e) => patch({ max_tokens: Number(e.target.value) })}
            />
          </Field>
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
            支持视觉（含图消息路由依赖此标志，须显式声明）
          </label>
          <Field label="排序权重" hint="列表展示顺序，小在前">
            <input
              type="number"
              className={INPUT_CLASS}
              value={draft.sort_order}
              onChange={(e) => patch({ sort_order: Number(e.target.value) })}
            />
          </Field>
          <Field label="描述">
            <input
              className={INPUT_CLASS}
              value={draft.description}
              onChange={(e) => patch({ description: e.target.value })}
            />
          </Field>
        </>
      ),
    },
  ]
}
