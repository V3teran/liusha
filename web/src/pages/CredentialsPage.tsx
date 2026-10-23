import { useEffect, useState } from 'react'
import { listCredentials, deleteCredentials } from '@/api/client'
import type { Identity } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'
import { AlertCircle, Key, Trash2 } from 'lucide-react'

// 凭证库页面：展示 Redis 中按 host 归属的用户凭证（Cookie/Authorization/自定义 header）。
// 数据源：后端 GET /credential?host=<host> 读 Redis HGETALL credentials:<host>。
// 写入：POST /credential/batch 批量保存；DELETE /credential?host=<host> 删除某 host 全部凭证。
// 布局：左侧 host 列表（从已有凭证推断）+ 右侧详情抽屉（展示该 host 下所有 identity）。
export default function CredentialsPage() {
  const [hosts, setHosts] = useState<string[]>([])
  const [selectedHost, setSelectedHost] = useState<string | null>(null)
  const [identities, setIdentities] = useState<Identity[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  // 初始化：从后端获取已有凭证的 host 列表
  // 注意：后端没有 GET /credential/hosts 端点，需要从其他地方推断
  // 这里暂时留空，等待后端补充接口或从流量/任务中提取 host
  useEffect(() => {
    // TODO: 实现 host 列表获取逻辑
    // 临时方案：可以从 GET /traffic/hosts 获取代理捕获过的 host
    setHosts([])
  }, [])

  // 加载某 host 的凭证
  const loadCredentials = async (host: string) => {
    setLoading(true)
    setError('')
    try {
      const data = await listCredentials(host)
      setIdentities(data)
      setSelectedHost(host)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }

  // 删除某 host 的全部凭证
  const handleDelete = async (host: string) => {
    if (!confirm(`确定删除 ${host} 的全部凭证？此操作不可恢复。`)) return

    try {
      await deleteCredentials(host)
      setHosts(hosts.filter(h => h !== host))
      if (selectedHost === host) {
        setSelectedHost(null)
        setIdentities([])
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  return (
    <>
      <ConfigListShell
        title="凭证库"
        subtitle="按主机归属的登录态与会话材料 · 存储于 Redis"
        loading={loading}
        error={error}
        empty={hosts.length === 0}
        emptyHint="暂无凭证 · 从代理捕获或手动录入"
      >
        {hosts.map((host) => (
          <ConfigRow
            key={host}
            name={host}
            description={`点击查看该主机下的全部身份凭证`}
            onClick={() => loadCredentials(host)}
            dimmed={false}
            right={
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation()
                  handleDelete(host)
                }}
                className="rounded p-1.5 text-muted transition-colors hover:bg-surface-hover hover:text-text"
                aria-label="删除凭证"
              >
                <Trash2 className="h-4 w-4" />
              </button>
            }
          />
        ))}
      </ConfigListShell>

      <ConfigDrawer
        open={!!selectedHost}
        title={selectedHost || '凭证详情'}
        onOpenChange={(o) => !o && setSelectedHost(null)}
        onSave={() => {}}
        tabs={
          selectedHost
            ? [
                {
                  value: 'identities',
                  label: `身份列表 (${identities.length})`,
                  content: (
                    <div className="space-y-4">
                      {identities.length === 0 ? (
                        <div className="flex items-center gap-2 rounded-lg border border-border bg-surface p-4 text-sm text-muted">
                          <AlertCircle className="h-4 w-4" />
                          该主机暂无凭证
                        </div>
                      ) : (
                        identities.map((id, idx) => (
                          <div key={idx} className="space-y-3 rounded-lg border border-border bg-surface p-4">
                            <div className="flex items-center justify-between">
                              <div className="flex items-center gap-2">
                                <Key className="h-4 w-4 text-accent" />
                                <span className="font-semibold text-text">{id.name}</span>
                              </div>
                              <Badge variant="outline">{id.credentials.length} 项</Badge>
                            </div>

                            {id.credentials.map((cred, credIdx) => (
                              <Field key={credIdx} label={cred.key}>
                                <textarea
                                  className={INPUT_CLASS + ' min-h-[4rem] resize-y font-mono text-[12px]'}
                                  value={cred.value}
                                  disabled
                                />
                              </Field>
                            ))}
                          </div>
                        ))
                      )}
                    </div>
                  ),
                },
              ]
            : []
        }
      />
    </>
  )
}
