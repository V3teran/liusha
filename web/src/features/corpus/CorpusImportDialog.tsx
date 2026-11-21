import { useId, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { errorText } from '@/api/client'
import { previewCorpusImport, type CorpusImportSection } from '@/api/corpus'
import { Badge } from '@/components/ui/Badge'
import { FormDialog } from '@/components/ui/FormDialog'
import { CorpusScopeSelect } from './CorpusScopeSelect'
import { useImportCorpus } from './useCorpusQueries'

interface CorpusImportDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onImported: (result: { imported: number; duplicates: number }) => void
}

const MAX_FILES = 20

// 导入分两步：选文件 → 预览切分结果（每个标题下的内容成为一条，过长的小节按段落拆分，
// 与已有正文相同的标为跳过）→ 确认导入。切分规则看结果比读说明直观，也免得导入后逐条删除。
export function CorpusImportDialog({ open, onOpenChange, onImported }: CorpusImportDialogProps) {
  const [files, setFiles] = useState<File[]>([])
  const [scenario, setScenario] = useState<string | null>(null)
  const preview = useMutation({
    mutationFn: (fs: File[]) => previewCorpusImport(fs),
  })
  const importer = useImportCorpus()
  const fileId = useId()
  const scopeId = useId()
  const sections: CorpusImportSection[] | undefined = preview.data
  const fresh = sections?.filter((s) => !s.duplicate).length ?? 0

  const close = (next: boolean) => {
    if (!next) {
      setFiles([])
      setScenario(null)
      preview.reset()
      importer.reset()
    }
    onOpenChange(next)
  }

  const failure = preview.isError
    ? errorText(preview.error, '预览失败')
    : importer.isError
      ? errorText(importer.error, '导入失败')
      : undefined

  return (
    <FormDialog
      open={open}
      onOpenChange={close}
      title="导入 Markdown"
      submitLabel={sections ? `导入 ${fresh} 条` : '预览'}
      canSubmit={sections ? fresh > 0 : files.length > 0 && files.length <= MAX_FILES}
      pending={preview.isPending || importer.isPending}
      error={failure}
      onSubmit={() =>
        sections
          ? importer.mutate(
              { files, scenario },
              {
                onSuccess: (res) => {
                  close(false)
                  onImported(res)
                },
              },
            )
          : preview.mutate(files)
      }
    >
      <div className="flex flex-col gap-1.5">
        <label htmlFor={scopeId} className="text-xs font-medium text-text-secondary">
          适用范围
        </label>
        <CorpusScopeSelect id={scopeId} value={scenario} onChange={setScenario} />
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor={fileId} className="text-xs font-medium text-text-secondary">
          文件
        </label>
        <input
          id={fileId}
          type="file"
          accept=".md,text/markdown"
          multiple
          className="text-sm text-text-secondary file:mr-3 file:rounded-md file:border file:border-border file:bg-surface file:px-3 file:py-1.5 file:text-sm file:text-text hover:file:border-border-strong"
          onChange={(e) => {
            setFiles(Array.from(e.target.files ?? []))
            preview.reset() // 换了文件，预览作废
          }}
        />
        {files.length > MAX_FILES && (
          <p role="alert" className="text-xs text-sev-critical">
            一次最多导入 {MAX_FILES} 个文件
          </p>
        )}
      </div>
      {sections && (
        <section aria-label="切分结果" className="flex flex-col gap-2">
          <p className="text-xs text-text-secondary">
            切出 {sections.length} 条
            {sections.length > fresh && `，其中 ${sections.length - fresh} 条与已有正文相同、导入时跳过`}
          </p>
          <ol
            tabIndex={0}
            className="flex max-h-64 flex-col divide-y divide-border-subtle overflow-y-auto rounded-lg border border-border"
          >
            {sections.map((s, i) => (
              <li key={i} className={s.duplicate ? 'px-3 py-2 opacity-60' : 'px-3 py-2'}>
                <p className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-sm text-text">{s.title}</span>
                  {s.duplicate && (
                    <Badge dot={false} size="sm" color="var(--status-neutral)">
                      跳过
                    </Badge>
                  )}
                </p>
                <p className="truncate text-xs text-text-tertiary">
                  {files.length > 1 && `${s.file} · `}
                  {s.content.replace(/\s+/g, ' ')}
                </p>
              </li>
            ))}
          </ol>
        </section>
      )}
    </FormDialog>
  )
}
