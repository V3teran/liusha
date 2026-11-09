import { SearchInput } from '@/components/SearchInput'
import { PageHeader } from '@/components/PageHeader'
import { FilterBar } from '@/components/FilterBar'
import { Field } from '@/features/config/ConfigDrawer'
import { OverrideStatus } from '@/features/config/OverrideStatus'
import { SettingsSection } from '@/features/settings/SettingsSection'
import { useQueryClient } from '@tanstack/react-query'
import { useSettingSection, type SettingSection } from '@/features/settings/useSettingSection'
import { linesToList, listToLines, codesToText, textToCodes } from '@/features/settings/listText'
import { useState, useMemo, type ReactNode } from 'react'
import { INPUT_CLASS, TEXTAREA_CLASS } from '@/components/ui/control'

const CODE_TEXTAREA_CLASS = TEXTAREA_CLASS + ' min-h-[76px] font-mono'

// 系统配置页：三组设置（会话压缩 / 工具运行时 / 代理流量过滤）分区独立保存。
// 出厂默认来自程序与 config.yaml，这里编辑的是用户自定义：只提交改动的字段，与出厂
// 相同的值不算自定义，每个已自定义字段可对比出厂、单独恢复默认。保存后经后端失效广播
// 生效——runner 现读即新值，proxy 热换过滤链（无需重启）。
export function SystemConfigPage() {
  const [query, setQuery] = useState('')

  // 定义各分区的元数据，用于搜索匹配
  const sections = useMemo(() => [
    {
      id: 'compaction',
      title: '上下文压缩',
      keywords: '上下文 压缩 历史 token 预算 占比 规划 执行 裁决 监察 纪要',
      component: <CompactionSection />,
    },
    {
      id: 'runtime',
      title: '工具运行时',
      keywords: '工具 运行时 超时 看门狗 llm 输出 尾部 截尾 字节 钳制',
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
      <PageHeader title="系统配置" subtitle="上下文压缩、代理过滤与工具运行时参数，保存即生效" />

      {/* 搜索归筛选栏（全站筛选控件唯一位置）：放进页头右槽会在窄屏把标题挤到零宽。 */}
      <FilterBar label="系统配置筛选">
        <SearchInput
          className="w-full sm:w-72"
          value={query}
          onChange={setQuery}
          placeholder="搜索配置项"
          ariaLabel="搜索配置项"
        />
      </FilterBar>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-4xl flex-col gap-6 px-6 py-6">
          {filteredSections.length === 0 ? (
            <div className="py-12 text-center text-sm text-text-secondary">
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

// SettingStatus 是设置字段的自定义状态条（未自定义时不渲染）。
function SettingStatus<T>({ s, field }: { s: SettingSection<T>; field: keyof T & string }) {
  const d = s.detail
  if (!d) return null
  return (
    <OverrideStatus
      state={d.overrides[field]}
      current={d.values[field]}
      busy={s.resetting === field}
      onReset={() => {
        if (window.confirm('把该项恢复为出厂默认？')) void s.resetField(field)
      }}
    />
  )
}

// sectionProps 是分区卡的通用属性（加载 / 保存 / 放弃 / 全部恢复出厂）。
function sectionProps<T>(s: SettingSection<T>) {
  return {
    loading: s.loading,
    error: s.error,
    saving: s.saving,
    saved: s.saved,
    isDirty: s.isDirty,
    onSave: () => void s.onSave(),
    onDiscard: s.discard,
    customizedCount: s.detail ? Object.keys(s.detail.overrides).length : 0,
    resettingAll: s.resetting === 'all',
    onResetAll: () => {
      if (window.confirm('把本组全部自定义恢复为出厂默认？')) void s.resetField()
    },
  }
}

// ── compaction（会话历史压缩）────────────────────────────────────────

function CompactionSection() {
  const s = useSettingSection('compaction')
  const d = s.draft
  const invalid =
    !d ||
    d.planner_context_ratio <= 0 ||
    d.executor_context_ratio <= 0 ||
    d.evaluator_context_ratio <= 0 ||
    d.monitor_context_ratio <= 0 ||
    d.planner_context_ratio > 0.9 ||
    d.executor_context_ratio > 0.9 ||
    d.evaluator_context_ratio > 0.9 ||
    d.monitor_context_ratio > 0.9

  return (
    <SettingsSection
      title="上下文压缩"
      subtitle="各角色 ReAct 循环的历史 token 预算 = 占比 × 该角色路由模型的 context_window；超预算的前缀被裁并蒸馏成纪要，占比越大上下文越完整、token 越贵"
      saveDisabled={invalid}
      {...sectionProps(s)}
    >
      {d && (
        <>
          <RatioField
            label="规划占比"
            aside={<SettingStatus s={s} field="planner_context_ratio" />}
            hint="规划链路长、上下文价值高，占比最大"
            value={d.planner_context_ratio}
            onChange={(v) => s.patch({ planner_context_ratio: v })}
          />
          <RatioField
            label="执行占比"
            aside={<SettingStatus s={s} field="executor_context_ratio" />}
            hint="执行工具循环的中档预算"
            value={d.executor_context_ratio}
            onChange={(v) => s.patch({ executor_context_ratio: v })}
          />
          <RatioField
            label="裁决占比"
            aside={<SettingStatus s={s} field="evaluator_context_ratio" />}
            hint="单假设取证链路短，预算最小"
            value={d.evaluator_context_ratio}
            onChange={(v) => s.patch({ evaluator_context_ratio: v })}
          />
          <RatioField
            label="监察占比"
            aside={<SettingStatus s={s} field="monitor_context_ratio" />}
            hint="周期性状态评估，链路最短"
            value={d.monitor_context_ratio}
            onChange={(v) => s.patch({ monitor_context_ratio: v })}
          />
        </>
      )}
    </SettingsSection>
  )
}

// RatioField 是历史预算占比输入（0 < 比例 ≤ 0.9，步进 0.05）。
function RatioField({
  label,
  hint,
  aside,
  value,
  onChange,
}: {
  label: string
  hint: string
  aside: ReactNode
  value: number
  onChange: (v: number) => void
}) {
  return (
    <Field label={label} hint={`${hint}，>0 且 ≤0.9`} aside={aside}>
      <input
        type="number"
        min={0.05}
        max={0.9}
        step={0.05}
        className={INPUT_CLASS}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
      />
    </Field>
  )
}

// ── runtime（工具运行时）─────────────────────────────────────────────

function RuntimeSection() {
  const queryClient = useQueryClient()
  // onSaved 失效 LLM 审计页的 runtime 缓存（useRuntimeSettings 同 key）：
  // 看门狗秒数改完，审计页的超时判定/展示立即可见，不再等 60s staleTime。
  const s = useSettingSection('runtime', () => {
    void queryClient.invalidateQueries({ queryKey: ['settings', 'runtime'] })
  })
  const d = s.draft
  const invalid =
    !d ||
    d.step_tool_timeout_seconds <= 0 ||
    d.step_llm_timeout_seconds <= 0 ||
    d.run_tail_bytes <= 0 ||
    d.tool_output_tail_bytes <= 0

  return (
    <SettingsSection
      title="工具运行时"
      subtitle="run_command 超时钳制、单步 LLM 看门狗与输出尾部保留——保存后 runner 现读即生效"
      saveDisabled={invalid}
      {...sectionProps(s)}
    >
      {d && (
        <>
          <Field label="工具超时钳制（秒）" hint="run_command 超时上界：LLM 自传更长的 timeout 会被截到此值，>0" aside={<SettingStatus s={s} field="step_tool_timeout_seconds" />}>
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.step_tool_timeout_seconds}
              onChange={(e) => s.patch({ step_tool_timeout_seconds: Number(e.target.value) })}
            />
          </Field>
          <Field label="LLM 看门狗（秒）" hint="单步 LLM 调用超时即失败；审计页「超时」判定与此同源，>0" aside={<SettingStatus s={s} field="step_llm_timeout_seconds" />}>
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.step_llm_timeout_seconds}
              onChange={(e) => s.patch({ step_llm_timeout_seconds: Number(e.target.value) })}
            />
          </Field>
          <Field label="输出尾部保留（字节）" hint="run_command stdout/stderr 保留尾部字节（结果在末段，头部噪音先弃），>0" aside={<SettingStatus s={s} field="run_tail_bytes" />}>
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.run_tail_bytes}
              onChange={(e) => s.patch({ run_tail_bytes: Number(e.target.value) })}
            />
          </Field>
          <Field label="工具输出全局钳制（字节）" hint="任意工具输出进上下文前的尾部保留上限——最后一道兜底，保证单条结果有界不撑爆上下文，>0" aside={<SettingStatus s={s} field="tool_output_tail_bytes" />}>
            <input
              type="number"
              min={1}
              className={INPUT_CLASS}
              value={d.tool_output_tail_bytes}
              onChange={(e) => s.patch({ tool_output_tail_bytes: Number(e.target.value) })}
            />
          </Field>
        </>
      )}
    </SettingsSection>
  )
}

// ── proxy_filter（代理流量过滤规则）──────────────────────────────────

function ProxyFilterSection() {
  const s = useSettingSection('proxy_filter')
  const d = s.draft
  const invalid = !d || d.max_request_body_size <= 0 || d.max_response_body_size <= 0

  return (
    <SettingsSection
      title="代理流量过滤规则"
      subtitle="代理捕获流量的黑白名单与请求/响应体切片上限，保存后 proxy 进程热换过滤链"
      saveDisabled={invalid}
      {...sectionProps(s)}
    >
      {d && (
        <>
          <Field label="放行 host 白名单" hint="每行一项；非空则仅捕获这些 host（留空 = 不限）" aside={<SettingStatus s={s} field="allow_hosts" />}>
            <textarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              placeholder="*.target.com"
              value={listToLines(d.allow_hosts)}
              onChange={(e) => s.patch({ allow_hosts: linesToList(e.target.value) })}
            />
          </Field>
          <Field label="排除 host 黑名单" hint="每行一项" aside={<SettingStatus s={s} field="exclude_hosts" />}>
            <textarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              value={listToLines(d.exclude_hosts)}
              onChange={(e) => s.patch({ exclude_hosts: linesToList(e.target.value) })}
            />
          </Field>
          <Field label="排除 HTTP 方法" hint="每行一项，如 OPTIONS / HEAD / CONNECT" aside={<SettingStatus s={s} field="exclude_methods" />}>
            <textarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              value={listToLines(d.exclude_methods)}
              onChange={(e) => s.patch({ exclude_methods: linesToList(e.target.value) })}
            />
          </Field>
          <Field label="排除 Upgrade 协议" hint="每行一项，如 websocket" aside={<SettingStatus s={s} field="exclude_upgrade_protocols" />}>
            <textarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              value={listToLines(d.exclude_upgrade_protocols)}
              onChange={(e) =>
                s.patch({ exclude_upgrade_protocols: linesToList(e.target.value) })
              }
            />
          </Field>
          <Field label="排除 URL 后缀" hint="每行一项，如 .css / .js / .png" aside={<SettingStatus s={s} field="exclude_suffixes" />}>
            <textarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              value={listToLines(d.exclude_suffixes)}
              onChange={(e) => s.patch({ exclude_suffixes: linesToList(e.target.value) })}
            />
          </Field>
          <Field label="排除 Content-Type" hint="每行一项，如 image/png" aside={<SettingStatus s={s} field="exclude_content_types" />}>
            <textarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              value={listToLines(d.exclude_content_types)}
              onChange={(e) => s.patch({ exclude_content_types: linesToList(e.target.value) })}
            />
          </Field>
          <Field label="排除响应状态码" hint="逗号/空格分隔，如 204, 304" aside={<SettingStatus s={s} field="exclude_status_codes" />}>
            <input
              className={INPUT_CLASS}
              spellCheck={false}
              placeholder="204, 304"
              value={codesToText(d.exclude_status_codes)}
              onChange={(e) => s.patch({ exclude_status_codes: textToCodes(e.target.value) })}
            />
          </Field>
          <div className="flex gap-3">
            <Field label="请求体上限（字节）" hint=">0" aside={<SettingStatus s={s} field="max_request_body_size" />}>
              <input
                type="number"
                min={1}
                className={INPUT_CLASS}
                value={d.max_request_body_size}
                onChange={(e) => s.patch({ max_request_body_size: Number(e.target.value) })}
              />
            </Field>
            <Field label="响应体上限（字节）" hint=">0" aside={<SettingStatus s={s} field="max_response_body_size" />}>
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
