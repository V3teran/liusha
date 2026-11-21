import { useState, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { Download, X } from 'lucide-react'
import { PROXY_CA_URL } from '@/api/proxy'
import { errorText } from '@/api/client'
import type { ProxyStatus } from '@/api/types'
import { Button, buttonVariants } from '@/components/ui/Button'
import { CopyButton } from '@/components/CopyButton'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { fullTime, relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { proxyAddress } from './proxyAddress'
import { useProxyStatus } from './useProxyStatus'

/** 代理的三种可见状态：运行中、已停止（运行过）、未启动（从未运行过，没有地址与证书）。 */
type Phase = 'online' | 'stopped' | 'never'

function phaseOf(s: ProxyStatus | undefined): Phase {
  if (s?.state === 'online') return 'online'
  return s?.last_seen_at ? 'stopped' : 'never'
}

const PHASE_META: Record<Phase, { label: string; dot: string }> = {
  online: { label: '运行中', dot: 'bg-status-success' },
  stopped: { label: '已停止', dot: 'bg-status-error' },
  never: { label: '未启动', dot: 'bg-status-neutral' },
}

/**
 * 「代理接入」：页头按钮带代理状态灯，点开是接入说明——代理地址、根证书下载与安装步骤。
 * 代理流量页与对话页「代理捕获」共用；状态每 15 秒刷新。
 */
export function ProxyAccess() {
  const [open, setOpen] = useState(false)
  const query = useProxyStatus()
  const phase = phaseOf(query.data)
  const meta = PHASE_META[phase]
  // 状态读不到时不显示状态灯，免得把「读不到」误报成「已停止」。
  const known = query.isSuccess

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <Button variant="secondary" aria-label={known ? `代理接入（代理${meta.label}）` : '代理接入'}>
          {known && <span aria-hidden className={cn('h-2 w-2 rounded-full', meta.dot)} />}
          代理接入
        </Button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content className="tac-modal fixed left-1/2 top-1/2 max-h-[calc(100vh-32px)] w-[560px] max-w-[calc(100vw-32px)] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-xl border border-border bg-surface shadow-lg outline-none">
          <div className="flex items-center justify-between border-b border-border px-5 py-3.5">
            <Dialog.Title className="text-sm font-semibold text-text">代理接入</Dialog.Title>
            <Dialog.Close asChild>
              <button
                type="button"
                aria-label="关闭"
                className="rounded-md p-1 text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text"
              >
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </div>
          <Dialog.Description className="sr-only">代理的运行状态、代理地址与根证书安装</Dialog.Description>
          <div className="flex flex-col gap-5 px-5 py-4">
            {query.isError ? (
              <p role="alert" className="text-sm text-sev-critical">
                ⚠ {errorText(query.error, '读取代理状态失败')}
              </p>
            ) : query.isPending ? (
              <p className="text-sm text-text-secondary">加载中…</p>
            ) : (
              <ProxyAccessBody status={query.data} phase={phase} />
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

function ProxyAccessBody({ status, phase }: { status: ProxyStatus; phase: Phase }) {
  const { copiedKey, copy } = useCopyToClipboard()
  const meta = PHASE_META[phase]

  if (phase === 'never') {
    return (
      <StatusLine dot={meta.dot} label={meta.label}>
        代理还没有运行过。启动 proxy 组件后，这里会显示代理地址与根证书。
      </StatusLine>
    )
  }

  const address = proxyAddress(status.public_addr, window.location.hostname)
  return (
    <>
      <StatusLine dot={meta.dot} label={meta.label}>
        {phase === 'online'
          ? status.last_request_at
            ? `最近收到请求：${relativeTime(status.last_request_at)}`
            : '尚未收到请求：按下面的步骤设置浏览器代理'
          : `最后在线：${fullTime(status.last_seen_at ?? '')}`}
      </StatusLine>

      <Section title="1. 设置代理">
        <div className="flex items-center gap-1 rounded-lg border border-border bg-surface-2 py-1 pl-3 pr-1">
          <code className="min-w-0 flex-1 truncate font-mono text-sm text-text">{address}</code>
          <CopyButton size="sm" copied={copiedKey === 'addr'} onClick={() => void copy('addr', address)} />
        </div>
        <p className="text-xs leading-5 text-text-secondary">
          在浏览器或系统的代理设置里，把 HTTP 与 HTTPS 代理都设为这个地址，然后浏览目标站。
        </p>
      </Section>

      <Section title="2. 安装并信任根证书">
        <a href={PROXY_CA_URL} download="liusha-ca.crt" className={cn(buttonVariants(), 'self-start')}>
          <Download />
          下载根证书
        </a>
        <p className="text-xs leading-5 text-text-secondary">
          不信任这张证书，浏览器会拦下所有 HTTPS 站点。核对已安装的证书时看 SHA-256 指纹：
        </p>
        <div className="flex items-start gap-1 rounded-lg border border-border bg-surface-2 py-1 pl-3 pr-1">
          <code className="min-w-0 flex-1 break-all py-1 font-mono text-[11.5px] leading-5 text-text-secondary">
            {status.ca_fingerprint}
          </code>
          <CopyButton size="sm" copied={copiedKey === 'fp'} onClick={() => void copy('fp', status.ca_fingerprint)} />
        </div>
        <ul className="flex flex-col gap-1.5 text-xs leading-5 text-text-secondary">
          <InstallStep os="Windows">双击证书，「安装证书」，存储位置选「受信任的根证书颁发机构」。</InstallStep>
          <InstallStep os="macOS">双击加入「钥匙串访问」，打开「liusha CA」，信任设为「始终信任」。</InstallStep>
          <InstallStep os="Linux · Chrome">设置 → 隐私和安全 → 安全 → 管理证书 → 授权机构 → 导入。</InstallStep>
          <InstallStep os="Firefox">
            使用自己的证书库（各系统都是）：设置 → 隐私与安全 → 查看证书 → 证书颁发机构 → 导入，勾选「信任由此证书颁发机构来标识网站」。
          </InstallStep>
          <InstallStep os="Android / iOS">
            在系统设置里安装证书；iOS 还要在「通用 → 关于本机 → 证书信任设置」里打开完全信任。
          </InstallStep>
        </ul>
      </Section>
    </>
  )
}

function StatusLine({ dot, label, children }: { dot: string; label: string; children: ReactNode }) {
  return (
    <div role="status" className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
      <span aria-hidden className={cn('h-2 w-2 rounded-full', dot)} />
      <span className="font-medium text-text">代理{label}</span>
      <span className="text-text-secondary">{children}</span>
    </div>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-[13px] font-semibold text-text">{title}</h3>
      {children}
    </section>
  )
}

function InstallStep({ os, children }: { os: string; children: ReactNode }) {
  return (
    <li className="flex gap-2">
      <span className="w-[92px] flex-shrink-0 font-medium text-text">{os}</span>
      <span className="min-w-0">{children}</span>
    </li>
  )
}
