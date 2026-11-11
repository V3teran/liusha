import { Field } from '@/features/config/ConfigDrawer'
import { INPUT_CLASS, TEXTAREA_CLASS, NUMBER_INPUT_CLASS } from '@/components/ui/control'
import { SettingsSection } from './SettingsSection'
import { useSettingSection } from './useSettingSection'
import { SettingStatus, sectionProps } from './sectionBindings'
import { linesToList, listToLines, codesToText, textToCodes } from './listText'

const CODE_TEXTAREA_CLASS = TEXTAREA_CLASS + ' min-h-[76px] font-mono'

export function ProxyFilterSection() {
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
                className={NUMBER_INPUT_CLASS}
                value={d.max_request_body_size}
                onChange={(e) => s.patch({ max_request_body_size: Number(e.target.value) })}
              />
            </Field>
            <Field label="响应体上限（字节）" hint=">0" aside={<SettingStatus s={s} field="max_response_body_size" />}>
              <input
                type="number"
                min={1}
                className={NUMBER_INPUT_CLASS}
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
