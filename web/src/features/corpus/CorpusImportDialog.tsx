import { useId, useState } from 'react'
import { errorText } from '@/api/client'
import { FormDialog } from '@/components/ui/FormDialog'
import { CorpusScopeSelect } from './CorpusScopeSelect'
import { useImportCorpus } from './useCorpusQueries'

interface CorpusImportDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onImported: (result: { imported: number; duplicates: number }) => void
}

const MAX_FILES = 20

export function CorpusImportDialog({ open, onOpenChange, onImported }: CorpusImportDialogProps) {
  const [files, setFiles] = useState<File[]>([])
  const [scenario, setScenario] = useState<string | null>(null)
  const importer = useImportCorpus()
  const fileId = useId()
  const scopeId = useId()

  const close = (next: boolean) => {
    if (!next) {
      setFiles([])
      setScenario(null)
      importer.reset()
    }
    onOpenChange(next)
  }

  return (
    <FormDialog
      open={open}
      onOpenChange={close}
      title="导入 Markdown"
      description="每个标题下的内容成为一条知识，标题即条目标题；过长的小节按段落拆分。与已有正文相同的跳过。"
      submitLabel="导入"
      canSubmit={files.length > 0 && files.length <= MAX_FILES}
      pending={importer.isPending}
      error={importer.isError ? errorText(importer.error, '导入失败') : undefined}
      onSubmit={() =>
        importer.mutate(
          { files, scenario },
          {
            onSuccess: (res) => {
              close(false)
              onImported(res)
            },
          },
        )
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
          onChange={(e) => setFiles(Array.from(e.target.files ?? []))}
        />
        {files.length > MAX_FILES && (
          <p role="alert" className="text-xs text-sev-critical">
            一次最多导入 {MAX_FILES} 个文件
          </p>
        )}
      </div>
    </FormDialog>
  )
}
