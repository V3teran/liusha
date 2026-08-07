import { SlidersHorizontal } from 'lucide-react'
import {
  getReactSettings,
  saveReactSettings,
  getRuntimeSettings,
  saveRuntimeSettings,
  getProxyFilterSettings,
  saveProxyFilterSettings,
} from '@/api/settings'
import type {
  ReactSettings,
  RuntimeSettings,
  ProxyFilterSettings,
} from '@/api/types'
import { Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'
import { SettingsSection } from '@/features/settings/SettingsSection'
import { useSettingSection } from '@/features/settings/useSettingSection'
import { linesToList, listToLines, codesToText, textToCodes } from '@/features/settings/listText'

const TEXTAREA_CLASS = INPUT_CLASS + ' min-h-[76px] font-mono leading-relaxed'

// 系统配置页：三组业务旋钮（会话压缩 / 工具运行时 / 代理流量过滤）分区独立保存。
// 事实源在 DB，写经后端失效广播即热改——react/runtime 令 runner 现读即生效，
// proxy_filter 触发 proxy 进程热换过滤链（无需重启）。故非「列表」而是「表单」形态。
export function SystemConfig() {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center gap-3 border-b border-border px-6 py-4">
        <SlidersHorizontal className="h-5 w-5 text-accent" />
        <div className="min-w-0">
          <h1 className="tac-prompt font-mono text-[15px] font-semibold text-text">系统配置</h1>
          <p className="mt-0.5 text-[12.5px] text-muted">
            运行期业务旋钮，保存即经多级缓存失效总线热生效，无需重启进程
          </p>
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
          <ReactSection />
          <RuntimeSection />
          <ProxyFilterSection />
        </div>
      </div>
    </div>
  )
}

// ── react（会话历史压缩）─────────────────────────────────────────────

function ReactSection() {
  const s = useSettingSection<ReactSettings>(getReactSettings, saveReactSettings)
  const d = s.draft
  const invalid =
    !d ||
    d.trigger_ratio <= 0 ||
    d.trigger_ratio > 1 ||
    d.trailing_budget_ratio <= 0 ||
    d.trailing_budget_ratio > 1 ||
    d.compactor_timeout_seconds <= 0

  return (
    <SettingsSection
      title="会话历史压缩"
      subtitle="ReAct 循环上下文接近窗口时的触发阈值与蒸馏预算"
      loading={s.loading}
      error={s.error}
      saving={s.saving}
      saved={s.saved}
      saveDisabled={invalid}
      onSave={() => void s.onSave()}
    >
      {d && (
        <>
          <Field label="触发阈值" hint="上下文占窗口比例达此值触发压缩，(0,1]">
            <input
              type="number"
              step="0.05"
              min={0}
              max={1}
              className={INPUT_CLASS}
              value={d.trigger_ratio}
              onChange={(e) => s.patch({ trigger_ratio: Number(e.target.value) })}
            />
          </Field>
          <Field label="Trailing 预算比例" hint="压缩后保留的近期会话占窗口比例，(0,1]">
            <input
              type="number"
              step="0.05"
              min={0}
              max={1}
              className={INPUT_CLASS}
              value={d.trailing_budget_ratio}
              onChange={(e) => s.patch({ trailing_budget_ratio: Number(e.target.value) })}
            />
          </Field>
          <Field label="蒸馏超时（秒）" hint="旧会话蒸馏单次 LLM 调用超时，>0">
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.compactor_timeout_seconds}
              onChange={(e) => s.patch({ compactor_timeout_seconds: Number(e.target.value) })}
            />
          </Field>
        </>
      )}
    </SettingsSection>
  )
}

// ── runtime（工具运行时）─────────────────────────────────────────────

function RuntimeSection() {
  const s = useSettingSection<RuntimeSettings>(getRuntimeSettings, saveRuntimeSettings)
  const d = s.draft
  const invalid =
    !d ||
    d.step_tool_timeout_seconds <= 0 ||
    d.run_tail_bytes <= 0 ||
    d.findings_limit_in_prompt <= 0

  return (
    <SettingsSection
      title="工具运行时"
      subtitle="单步工具执行超时、输出截尾与 prompt 注入 finding 上限"
      loading={s.loading}
      error={s.error}
      saving={s.saving}
      saved={s.saved}
      saveDisabled={invalid}
      onSave={() => void s.onSave()}
    >
      {d && (
        <>
          <Field label="单步工具超时（秒）" hint="单次工具执行兜底超时，>0">
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.step_tool_timeout_seconds}
              onChange={(e) => s.patch({ step_tool_timeout_seconds: Number(e.target.value) })}
            />
          </Field>
          <Field label="输出截尾字节数" hint="run_command stdout/stderr 保留尾部字节，>0">
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.run_tail_bytes}
              onChange={(e) => s.patch({ run_tail_bytes: Number(e.target.value) })}
            />
          </Field>
          <Field label="Prompt finding 上限" hint="注入 prompt 的既有 finding DB 读上限，>0">
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.findings_limit_in_prompt}
              onChange={(e) => s.patch({ findings_limit_in_prompt: Number(e.target.value) })}
            />
          </Field>
        </>
      )}
    </SettingsSection>
  )
}

// ── proxy_filter（代理流量过滤规则）──────────────────────────────────

function ProxyFilterSection() {
  const s = useSettingSection<ProxyFilterSettings>(
    getProxyFilterSettings,
    saveProxyFilterSettings,
  )
  const d = s.draft
  const invalid = !d || d.max_request_body_size <= 0 || d.max_response_body_size <= 0

  return (
    <SettingsSection
      title="代理流量过滤规则"
      subtitle="被动代理捕获的黑白名单与请求/响应体切片上限，保存后 proxy 进程热换过滤链"
      loading={s.loading}
      error={s.error}
      saving={s.saving}
      saved={s.saved}
      saveDisabled={invalid}
      onSave={() => void s.onSave()}
    >
      {d && (
        <>
          <Field label="放行 host 白名单" hint="每行一项；非空则仅捕获这些 host（留空 = 不限）">
            <textarea
              className={TEXTAREA_CLASS}
              spellCheck={false}
              placeholder="*.target.com"
              value={listToLines(d.allow_hosts)}
              onChange={(e) => s.patch({ allow_hosts: linesToList(e.target.value) })}
            />
          </Field>
          <Field label="排除 host 黑名单" hint="每行一项">
            <textarea
              className={TEXTAREA_CLASS}
              spellCheck={false}
              value={listToLines(d.exclude_hosts)}
              onChange={(e) => s.patch({ exclude_hosts: linesToList(e.target.value) })}
            />
          </Field>
          <Field label="排除 HTTP 方法" hint="每行一项，如 OPTIONS / HEAD / CONNECT">
            <textarea
              className={TEXTAREA_CLASS}
              spellCheck={false}
              value={listToLines(d.exclude_methods)}
              onChange={(e) => s.patch({ exclude_methods: linesToList(e.target.value) })}
            />
          </Field>
          <Field label="排除 Upgrade 协议" hint="每行一项，如 websocket">
            <textarea
              className={TEXTAREA_CLASS}
              spellCheck={false}
              value={listToLines(d.exclude_upgrade_protocols)}
              onChange={(e) =>
                s.patch({ exclude_upgrade_protocols: linesToList(e.target.value) })
              }
            />
          </Field>
          <Field label="排除 URL 后缀" hint="每行一项，如 .css / .js / .png">
            <textarea
              className={TEXTAREA_CLASS}
              spellCheck={false}
              value={listToLines(d.exclude_suffixes)}
              onChange={(e) => s.patch({ exclude_suffixes: linesToList(e.target.value) })}
            />
          </Field>
          <Field label="排除 Content-Type" hint="每行一项，如 image/png">
            <textarea
              className={TEXTAREA_CLASS}
              spellCheck={false}
              value={listToLines(d.exclude_content_types)}
              onChange={(e) => s.patch({ exclude_content_types: linesToList(e.target.value) })}
            />
          </Field>
          <Field label="排除响应状态码" hint="逗号/空格分隔，如 204, 304">
            <input
              className={INPUT_CLASS}
              spellCheck={false}
              placeholder="204, 304"
              value={codesToText(d.exclude_status_codes)}
              onChange={(e) => s.patch({ exclude_status_codes: textToCodes(e.target.value) })}
            />
          </Field>
          <div className="flex gap-3">
            <Field label="请求体上限（字节）" hint=">0">
              <input
                type="number"
                min={1}
                className={INPUT_CLASS}
                value={d.max_request_body_size}
                onChange={(e) => s.patch({ max_request_body_size: Number(e.target.value) })}
              />
            </Field>
            <Field label="响应体上限（字节）" hint=">0">
              <input
                type="number"
                min={1}
                className={INPUT_CLASS}
                value={d.max_response_body_size}
                onChange={(e) => s.patch({ max_response_body_size: Number(e.target.value) })}
              />
            </Field>
          </div>
        </>
      )}
    </SettingsSection>
  )
}
