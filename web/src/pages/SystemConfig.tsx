import {
  getCompactionSettings,
  saveCompactionSettings,
  getRuntimeSettings,
  saveRuntimeSettings,
  getProxyFilterSettings,
  saveProxyFilterSettings,
} from '@/api/settings'
import type {
  CompactionSettings,
  RuntimeSettings,
  ProxyFilterSettings,
} from '@/api/types'
import { Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'
import { SettingsSection } from '@/features/settings/SettingsSection'
import { useSettingSection } from '@/features/settings/useSettingSection'
import { linesToList, listToLines, codesToText, textToCodes } from '@/features/settings/listText'
import { Search } from 'lucide-react'
import { useState, useMemo } from 'react'

const TEXTAREA_CLASS = INPUT_CLASS + ' min-h-[76px] font-mono leading-relaxed'

// 系统配置页：三组业务旋钮（会话压缩 / 工具运行时 / 代理流量过滤）分区独立保存。
// 事实源在 DB，写经后端失效广播即热改——compaction/runtime 令 runner 现读即生效，
// proxy_filter 触发 proxy 进程热换过滤链（无需重启）。故非「列表」而是「表单」形态。
export function SystemConfig() {
  const [query, setQuery] = useState('')

  // 定义各分区的元数据，用于搜索匹配
  const sections = useMemo(() => [
    {
      id: 'compaction',
      title: '会话历史压缩',
      keywords: '会话 历史 压缩 滚动 窗口 消息 条数 规划 执行 裁决 监察',
      component: <CompactionSection />,
    },
    {
      id: 'runtime',
      title: '工具运行时',
      keywords: '工具 运行时 超时 看门狗 llm 输出 尾部 截尾 字节',
      component: <RuntimeSection />,
    },
    {
      id: 'proxy_filter',
      title: '代理流量过滤规则',
      keywords: '代理 流量 过滤 黑名单 白名单 host 方法 协议 后缀 状态码',
      component: <ProxyFilterSection />,
    },
  ], [])

  const filteredSections = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return sections
    return sections.filter(s =>
      s.title.toLowerCase().includes(q) ||
      s.keywords.toLowerCase().includes(q)
    )
  }, [sections, query])

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b border-border bg-surface px-6 py-4">
        <div>
          <h1 className="text-base font-semibold text-text">系统配置</h1>
          <p className="mt-0.5 text-sm text-text-secondary">
            系统常规配置，热加载
          </p>
        </div>
        <div className="relative">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索配置项"
            aria-label="搜索配置项"
            spellCheck={false}
            className="w-48 rounded-lg border border-border bg-surface-2 py-1.5 pl-8 pr-2.5 text-xs text-text outline-none placeholder:text-faint focus:border-accent"
          />
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
          {filteredSections.length === 0 ? (
            <div className="py-12 text-center text-sm text-muted">
              未找到匹配的配置项
            </div>
          ) : (
            filteredSections.map(section => (
              <div key={section.id}>{section.component}</div>
            ))
          )}
        </div>
      </div>
    </div>
  )
}

// ── compaction（会话历史压缩）────────────────────────────────────────

function CompactionSection() {
  const s = useSettingSection<CompactionSettings>(getCompactionSettings, saveCompactionSettings)
  const d = s.draft
  const invalid =
    !d ||
    d.planner_window_messages <= 0 ||
    d.executor_window_messages <= 0 ||
    d.evaluator_window_messages <= 0 ||
    d.monitor_window_messages <= 0

  return (
    <SettingsSection
      title="会话历史压缩"
      subtitle="各角色 ReAct 循环的滚动窗口：每步保留 system + 最近 N 条非系统消息，N 越大上下文越完整、token 越贵"
      loading={s.loading}
      error={s.error}
      saving={s.saving}
      saved={s.saved}
      saveDisabled={invalid}
      isDirty={s.isDirty}
      onSave={() => void s.onSave()}
      onReset={s.reset}
    >
      {d && (
        <>
          <WindowField
            label="规划窗口（条）"
            hint="规划链路长、上下文价值高，窗口最大"
            value={d.planner_window_messages}
            onChange={(v) => s.patch({ planner_window_messages: v })}
          />
          <WindowField
            label="执行窗口（条）"
            hint="执行工具循环的中档窗口"
            value={d.executor_window_messages}
            onChange={(v) => s.patch({ executor_window_messages: v })}
          />
          <WindowField
            label="裁决窗口（条）"
            hint="单假设取证链路短，窗口最小"
            value={d.evaluator_window_messages}
            onChange={(v) => s.patch({ evaluator_window_messages: v })}
          />
          <WindowField
            label="监察窗口（条）"
            hint="周期性状态评估，链路最短"
            value={d.monitor_window_messages}
            onChange={(v) => s.patch({ monitor_window_messages: v })}
          />
        </>
      )}
    </SettingsSection>
  )
}

// WindowField 是滚动窗口条数输入（正整数）。
function WindowField({
  label,
  hint,
  value,
  onChange,
}: {
  label: string
  hint: string
  value: number
  onChange: (v: number) => void
}) {
  return (
    <Field label={label} hint={`${hint}，>0`}>
      <input
        type="number"
        min={1}
        className={INPUT_CLASS}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
      />
    </Field>
  )
}

// ── runtime（工具运行时）─────────────────────────────────────────────

function RuntimeSection() {
  const s = useSettingSection<RuntimeSettings>(getRuntimeSettings, saveRuntimeSettings)
  const d = s.draft
  const invalid =
    !d ||
    d.step_tool_timeout_seconds <= 0 ||
    d.step_llm_timeout_seconds <= 0 ||
    d.run_tail_bytes <= 0

  return (
    <SettingsSection
      title="工具运行时"
      subtitle="run_command 超时钳制、单步 LLM 看门狗与输出尾部保留——保存后 runner 现读即生效"
      loading={s.loading}
      error={s.error}
      saving={s.saving}
      saved={s.saved}
      saveDisabled={invalid}
      isDirty={s.isDirty}
      onSave={() => void s.onSave()}
      onReset={s.reset}
    >
      {d && (
        <>
          <Field label="工具超时钳制（秒）" hint="run_command 超时上界：LLM 自传更长的 timeout 会被截到此值，>0">
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.step_tool_timeout_seconds}
              onChange={(e) => s.patch({ step_tool_timeout_seconds: Number(e.target.value) })}
            />
          </Field>
          <Field label="LLM 看门狗（秒）" hint="单步 LLM 调用超时即失败；审计页「超时」判定与此同源，>0">
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.step_llm_timeout_seconds}
              onChange={(e) => s.patch({ step_llm_timeout_seconds: Number(e.target.value) })}
            />
          </Field>
          <Field label="输出尾部保留（字节）" hint="run_command stdout/stderr 保留尾部字节（结果在末段，头部噪音先弃），>0">
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.run_tail_bytes}
              onChange={(e) => s.patch({ run_tail_bytes: Number(e.target.value) })}
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
      isDirty={s.isDirty}
      onSave={() => void s.onSave()}
      onReset={s.reset}
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
