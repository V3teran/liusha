import { useMemo, useState } from 'react'
import * as Tabs from '@radix-ui/react-tabs'
import { CopyButton } from '@/components/CopyButton'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { canPretty, contentTypeFromRaw, prettyRaw } from '@/lib/rawMessage'

interface RawMessageBlockProps {
  title: string // 「请求」/「响应」
  raw: string // 整条报文原文（起始行 + 头 + 体一体）
  emptyHint: string // 无报文时的占位文案
}

const PRE_CLASS =
  'max-h-[38vh] overflow-auto whitespace-pre-wrap break-words rounded-b-lg border border-t-0 border-border bg-surface-2 px-3 py-2.5 font-mono text-[11.5px] leading-relaxed text-text'

// Burp 式整条报文块：一个标题栏（含 原文/美化 tab + 复制），下方等宽字体渲染整条报文文本。
// 「美化」仅在 body 为可解析 JSON 时出现；切到美化只格式化 body 段，头段原样保留。
// proxy_traffic 只读取证数据，无编辑；body 已在落库时截断。
export function RawMessageBlock({ title, raw, emptyHint }: RawMessageBlockProps) {
  const { copiedKey, copy } = useCopyToClipboard()
  // content-type 从报文头自解析：请求/响应各自据此判断能否美化（请求也带 Content-Type，故请求块同样可美化）。
  const contentType = useMemo(() => contentTypeFromRaw(raw), [raw])
  const showPretty = useMemo(() => canPretty(raw, contentType), [raw, contentType])
  const [mode, setMode] = useState<'raw' | 'pretty'>('raw')

  if (!raw) {
    return (
      <section className="min-w-0">
        <h3 className="mb-2 text-[13px] font-semibold text-text">{title}</h3>
        <div className="rounded-lg border border-border bg-surface-2 px-3 py-4 text-[12.5px] text-muted opacity-70">{emptyHint}</div>
      </section>
    )
  }

  const shown = mode === 'pretty' && showPretty ? prettyRaw(raw, contentType) : raw

  return (
    <section className="min-w-0">
      <Tabs.Root value={showPretty ? mode : 'raw'} onValueChange={(v) => setMode(v === 'pretty' ? 'pretty' : 'raw')}>
        <div className="flex items-center gap-2 rounded-t-lg border border-border bg-surface px-2.5 py-1.5">
          <h3 className="text-[13px] font-semibold text-text">{title}</h3>
          <Tabs.List className="ml-2 flex items-center gap-0.5 rounded-md bg-surface-2 p-0.5" aria-label={`${title}报文视图`}>
            <Tabs.Trigger
              value="raw"
              className="rounded px-2 py-0.5 text-[11px] text-muted transition-colors data-[state=active]:bg-surface data-[state=active]:text-text data-[state=active]:shadow-sm"
            >
              原文
            </Tabs.Trigger>
            {showPretty && (
              <Tabs.Trigger
                value="pretty"
                className="rounded px-2 py-0.5 text-[11px] text-muted transition-colors data-[state=active]:bg-surface data-[state=active]:text-text data-[state=active]:shadow-sm"
              >
                美化
              </Tabs.Trigger>
            )}
          </Tabs.List>
          <CopyButton
            copied={copiedKey === 'raw'}
            onClick={() => void copy('raw', shown)}
            className="ml-auto"
          />
        </div>
        <pre className={PRE_CLASS}>{shown}</pre>
      </Tabs.Root>
    </section>
  )
}
