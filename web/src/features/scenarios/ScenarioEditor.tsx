import { useEffect, useMemo, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { errorText } from '@/api/client'
import { createScenario, deleteScenario, patchScenario, resetScenario, type ScenarioPatch } from '@/api/config'
import type { ScenarioDetail } from '@/api/types'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { INPUT_CLASS, TEXTAREA_CLASS } from '@/components/ui/control'
import { ConfigDrawer, Field } from '@/features/config/ConfigDrawer'
import { OverrideStatus } from '@/features/config/OverrideStatus'
import { useDirtyDraft } from '@/hooks/useDirtyDraft'
import { SCENARIOS_KEY, useScenarioDetail } from './useScenarioQueries'

// 场景编辑抽屉：新建自建场景，或编辑一个已有场景（内置与自建同一表单）。内置场景可编辑、停用、
// 恢复出厂、删除（删除后可在「已删除的内置」里恢复）；被定时模板引用的场景不可删除、停用（服务端
// 给出原因）。

/** 名称与正文的长度上限（与后端 scenariostore.MaxNameRunes / MaxBodyRunes 同值）。 */
const MAX_NAME_RUNES = 64
const MAX_BODY_RUNES = 8000

interface ScenarioDraft {
  code: string
  name: string
  description: string
  body: string
  enabled: boolean
}

type ContentField = 'name' | 'description' | 'body' | 'enabled'
const CONTENT_FIELDS: ContentField[] = ['name', 'description', 'body', 'enabled']

const EMPTY_DRAFT: ScenarioDraft = { code: '', name: '', description: '', body: '', enabled: true }

const draftOf = (d: ScenarioDetail): ScenarioDraft => ({
  code: d.scenario.code,
  name: d.scenario.name,
  description: d.scenario.description,
  body: d.scenario.body,
  enabled: d.scenario.enabled,
})

const runeCount = (s: string) => Array.from(s).length

interface ScenarioEditorProps {
  /** 要编辑的场景；null = 新建。 */
  code: string | null
  /** 关闭抽屉。replace：系统自动关闭（如删除后、条目不存在），不新增历史。 */
  onClose: (opts?: { replace?: boolean }) => void
}

/** 「恢复出厂」待确认的范围：某个字段，或 'all' = 全部字段。 */
type PendingReset = ContentField | 'all'

export function ScenarioEditor({ code, onClose }: ScenarioEditorProps) {
  const qc = useQueryClient()
  const isNew = code === null
  const detailQuery = useScenarioDetail(code ?? '')
  const detail = detailQuery.data ?? null
  const { draft, patch, open, rebase, baseline } = useDirtyDraft<ScenarioDraft>()
  const [saving, setSaving] = useState(false)
  const [resetting, setResetting] = useState<PendingReset | null>(null)
  const [pendingReset, setPendingReset] = useState<PendingReset | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)

  // 草稿只在首次拿到内容时建立：之后详情因失效重取，不覆盖正在进行的编辑。
  useEffect(() => {
    if (draft) return
    if (isNew) open(EMPTY_DRAFT)
    else if (detail) open(draftOf(detail))
  }, [draft, isNew, detail, open])

  // 首次加载失败（链接指向的场景已被删除等）：没有可编辑的内容，提示后自动关闭。内容已加载后
  // 的后台重取失败不关闭——查询保留已有数据，草稿照常编辑。
  const loadFailed = !!detailQuery.error && !detailQuery.data
  useEffect(() => {
    if (!loadFailed) return
    window.alert(errorText(detailQuery.error, '加载场景失败'))
    onClose({ replace: true })
  }, [loadFailed, detailQuery.error, onClose])

  // 写请求返回时本编辑器可能已被替换（后退 / 前进切到了别的场景）：只有仍在的编辑器才关闭抽屉，
  // 迟到的结果不得关掉之后打开的编辑器。
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  const changes = useMemo<ScenarioPatch>(() => {
    if (!draft || !baseline) return {}
    const c: ScenarioPatch = {}
    for (const k of CONTENT_FIELDS) {
      if (draft[k] !== baseline[k]) Object.assign(c, { [k]: draft[k] })
    }
    return c
  }, [draft, baseline])
  const dirty = isNew ? JSON.stringify(draft) !== JSON.stringify(EMPTY_DRAFT) : Object.keys(changes).length > 0

  const requestClose = () => {
    if (saving) return // 写请求在途：等它落定，结果决定是否关闭
    if (dirty && !window.confirm('有未保存的更改，确认丢弃？')) return
    onClose()
  }

  const nameRunes = draft ? runeCount(draft.name) : 0
  const bodyRunes = draft ? runeCount(draft.body) : 0
  const invalid =
    !draft ||
    !draft.name.trim() ||
    !draft.body.trim() ||
    nameRunes > MAX_NAME_RUNES ||
    bodyRunes > MAX_BODY_RUNES

  const invalidate = () => qc.invalidateQueries({ queryKey: SCENARIOS_KEY })

  const onSave = async () => {
    if (!draft) return
    setSaving(true)
    try {
      if (isNew) {
        await createScenario({
          name: draft.name.trim(),
          description: draft.description,
          body: draft.body,
        })
      } else {
        await patchScenario(draft.code, changes)
      }
      void invalidate()
      if (alive.current) onClose()
    } catch (e) {
      window.alert(errorText(e, '保存失败'))
    } finally {
      setSaving(false)
    }
  }

  const onDelete = async () => {
    if (!draft) return
    setSaving(true)
    try {
      await deleteScenario(draft.code)
      setConfirmDelete(false)
      void invalidate()
      if (alive.current) onClose({ replace: true })
    } catch (e) {
      setConfirmDelete(false)
      window.alert(errorText(e, '删除失败'))
    } finally {
      setSaving(false)
    }
  }

  // 恢复出厂是即时的服务端操作；其它字段上未保存的编辑保留。
  const onReset = async (scope: PendingReset) => {
    if (!draft || !baseline) return
    setPendingReset(null)
    setResetting(scope)
    const field = scope === 'all' ? undefined : scope
    try {
      const fresh = await resetScenario(draft.code, field)
      qc.setQueryData([...SCENARIOS_KEY, 'detail', draft.code], fresh)
      void invalidate()
      const next = draftOf(fresh)
      rebase(next, (cur) => {
        if (!field) return next
        const merged = { ...next }
        for (const k of CONTENT_FIELDS) {
          if (k !== field && cur[k] !== baseline[k]) Object.assign(merged, { [k]: cur[k] })
        }
        return merged
      })
    } catch (e) {
      window.alert(errorText(e, '恢复失败'))
    } finally {
      setResetting(null)
    }
  }

  const isBuiltin = !!detail?.scenario.is_builtin
  const isDefault = !!detail?.scenario.is_default
  const status = (field: ContentField, long = false) =>
    detail && (
      <OverrideStatus
        state={detail.overrides[field]}
        current={detail.scenario[field]}
        long={long}
        busy={resetting === field}
        format={field === 'enabled' ? (v) => (v ? '启用' : '停用') : undefined}
        onReset={() => setPendingReset(field)}
      />
    )
  const customizedCount = detail ? Object.keys(detail.overrides).length : 0

  return (
    <>
      <ConfigDrawer
        open={!!draft}
        title={isNew ? '新建场景' : draft?.name || '场景'}
        onOpenChange={(o) => !o && requestClose()}
        onSave={() => void onSave()}
        onDelete={isNew ? undefined : () => setConfirmDelete(true)}
        saving={saving}
        saveDisabled={saving || invalid || !dirty || resetting !== null}
        tabs={
          draft
            ? [
                {
                  value: 'info',
                  label: '基本信息',
                  content: (
                    <>
                      {detail?.shadows_builtin && (
                        <p className="text-xs text-status-warning">
                          这个自建场景与同名内置场景冲突，内置版本被遮蔽、不生效。
                        </p>
                      )}
                      <Field
                        label="名称"
                        hint={`智能体看到的场景名，也显示在下发入口的选择框里 · ${nameRunes} / ${MAX_NAME_RUNES} 字`}
                        aside={status('name')}
                      >
                        <input
                          className={INPUT_CLASS}
                          value={draft.name}
                          aria-invalid={nameRunes > MAX_NAME_RUNES}
                          onChange={(e) => patch({ name: e.target.value })}
                        />
                      </Field>
                      {nameRunes > MAX_NAME_RUNES && (
                        <p className="text-xs text-status-error">名称超过 {MAX_NAME_RUNES} 字上限。</p>
                      )}
                      <Field label="说明" hint="给人看的简介：选择场景时作为提示，不进智能体提示词" aside={status('description', true)}>
                        <textarea
                          className={TEXTAREA_CLASS + ' min-h-[4rem] resize-y'}
                          value={draft.description}
                          onChange={(e) => patch({ description: e.target.value })}
                        />
                      </Field>
                      {!isNew && (
                        <Field
                          label="启用"
                          hint={
                            isDefault
                              ? '缺省场景：下发不指定场景时使用。停用或删除后，缺省即无场景'
                              : '停用后不能再用于新任务；已建任务照常使用自己的场景快照'
                          }
                          aside={status('enabled')}
                        >
                          <span className="flex items-center gap-2 text-sm text-text">
                            <input
                              type="checkbox"
                              checked={draft.enabled}
                              onChange={(e) => patch({ enabled: e.target.checked })}
                            />
                            {draft.enabled ? '已启用' : '已停用'}
                          </span>
                        </Field>
                      )}
                      {customizedCount > 0 && (
                        <div className="flex items-center justify-between rounded-md border border-border px-3 py-2 text-xs text-text-secondary">
                          <span>已自定义 {customizedCount} 项（改过的内容不随版本更新）</span>
                          <button
                            type="button"
                            disabled={resetting !== null}
                            className="underline-offset-2 hover:text-accent hover:underline disabled:opacity-50"
                            onClick={() => setPendingReset('all')}
                          >
                            全部恢复出厂
                          </button>
                        </div>
                      )}
                    </>
                  ),
                },
                {
                  value: 'content',
                  label: '场景内容',
                  content: (
                    <div className="flex min-h-0 flex-1 flex-col gap-2">
                      {status('body', true)}
                      <Field
                        label="场景正文（Markdown）"
                        hint={`整份拼在四个智能体系统提示词的末尾 · ${bodyRunes} / ${MAX_BODY_RUNES} 字`}
                      >
                        <textarea
                          className={TEXTAREA_CLASS + ' min-h-[24rem] resize-y font-mono'}
                          value={draft.body}
                          aria-invalid={bodyRunes > MAX_BODY_RUNES}
                          onChange={(e) => patch({ body: e.target.value })}
                        />
                      </Field>
                      {bodyRunes > MAX_BODY_RUNES && (
                        <p className="text-xs text-status-error">正文超过 {MAX_BODY_RUNES} 字上限，请精简后保存。</p>
                      )}
                    </div>
                  ),
                },
              ]
            : []
        }
      />

      <ConfirmDialog
        open={pendingReset !== null}
        onOpenChange={(o) => !o && setPendingReset(null)}
        title="恢复出厂"
        message={`把${pendingReset === 'all' ? '全部字段' : '该项'}恢复为出厂内容？你的修改将被覆盖，不可撤销。`}
        danger
        confirmLabel="确认恢复"
        onConfirm={() => pendingReset && void onReset(pendingReset)}
      />
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="删除场景"
        message={
          isBuiltin
            ? `删除内置场景「${draft?.name ?? ''}」？升级不会再补回它，可在「已删除的内置」里恢复成出厂内容（你的修改不保留）。已建的任务照常使用自己的场景快照；被定时模板引用时不能删除。${isDefault ? '它是缺省场景，删除后缺省即无场景。' : ''}`
            : `删除场景「${draft?.name ?? ''}」？不可恢复。已建的任务照常使用自己的场景快照，不受影响；被定时模板引用时不能删除。`
        }
        danger
        confirmLabel="确认删除"
        onConfirm={() => void onDelete()}
      />
    </>
  )
}
