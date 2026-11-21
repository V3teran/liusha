import { Field } from '@/features/config/ConfigDrawer'
import { INPUT_CLASS, NUMBER_INPUT_CLASS } from '@/components/ui/control'
import { SettingsSection } from './SettingsSection'
import { useSettingSection } from './useSettingSection'
import { SettingStatus, sectionProps } from './sectionBindings'
import { ListTextarea, CodesInput } from './ListInputs'

// 最小高度容下三行占位示例
const CODE_TEXTAREA_CLASS = 'min-h-[76px] font-mono'

export function ProxyFilterSection() {
  const s = useSettingSection('proxy_filter')
  const d = s.draft
  const invalid = !d || d.max_request_body_bytes <= 0 || d.max_response_body_bytes <= 0

  return (
    <SettingsSection
      title="代理流量过滤规则"
      saveDisabled={invalid}
      {...sectionProps(s)}
    >
      {d && (
        <>
          <Field label="放行 host 白名单" aside={<SettingStatus s={s} field="allow_hosts" />}>
            <ListTextarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              placeholder={"*.target.com\napi.target.com\n留空即全部放行"}
              value={d.allow_hosts}
              onChange={(v) => s.patch({ allow_hosts: v })}
            />
          </Field>
          <Field label="排除 host 黑名单" aside={<SettingStatus s={s} field="exclude_hosts" />}>
            <ListTextarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              placeholder={"*.google.com\ncdn.example.com"}
              value={d.exclude_hosts}
              onChange={(v) => s.patch({ exclude_hosts: v })}
            />
          </Field>
          <Field label="排除 HTTP 方法" aside={<SettingStatus s={s} field="exclude_methods" />}>
            <ListTextarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              placeholder={"OPTIONS\nHEAD\nCONNECT"}
              value={d.exclude_methods}
              onChange={(v) => s.patch({ exclude_methods: v })}
            />
          </Field>
          <Field label="排除 Upgrade 协议" aside={<SettingStatus s={s} field="exclude_upgrade_protocols" />}>
            <ListTextarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              placeholder={"websocket"}
              value={d.exclude_upgrade_protocols}
              onChange={(v) => s.patch({ exclude_upgrade_protocols: v })}
            />
          </Field>
          <Field label="排除 URL 后缀" aside={<SettingStatus s={s} field="exclude_suffixes" />}>
            <ListTextarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              placeholder={".css\n.js\n.png"}
              value={d.exclude_suffixes}
              onChange={(v) => s.patch({ exclude_suffixes: v })}
            />
          </Field>
          <Field label="排除 Content-Type" aside={<SettingStatus s={s} field="exclude_content_types" />}>
            <ListTextarea
              className={CODE_TEXTAREA_CLASS}
              spellCheck={false}
              placeholder={"image/png\nfont/woff2"}
              value={d.exclude_content_types}
              onChange={(v) => s.patch({ exclude_content_types: v })}
            />
          </Field>
          <Field label="排除响应状态码" aside={<SettingStatus s={s} field="exclude_status_codes" />}>
            <CodesInput
              className={INPUT_CLASS}
              spellCheck={false}
              placeholder="204, 304"
              value={d.exclude_status_codes}
              onChange={(v) => s.patch({ exclude_status_codes: v })}
            />
          </Field>
          <div className="flex gap-3">
            <Field label="请求体上限（字节）" aside={<SettingStatus s={s} field="max_request_body_bytes" />}>
              <input
                type="number"
                min={1}
                className={NUMBER_INPUT_CLASS}
                value={d.max_request_body_bytes}
                onChange={(e) => s.patch({ max_request_body_bytes: Number(e.target.value) })}
              />
            </Field>
            <Field label="响应体上限（字节）" aside={<SettingStatus s={s} field="max_response_body_bytes" />}>
              <input
                type="number"
                min={1}
                className={NUMBER_INPUT_CLASS}
                value={d.max_response_body_bytes}
                onChange={(e) => s.patch({ max_response_body_bytes: Number(e.target.value) })}
              />
            </Field>
          </div>
        </>
      )}
    </SettingsSection>
  )
}
