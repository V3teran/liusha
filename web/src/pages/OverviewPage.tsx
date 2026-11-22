import { useSearchParams } from 'react-router-dom'
import { RotateCw } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/Button'
import { ErrorState } from '@/components/ui/ErrorState'
import { LoadingState } from '@/components/ui/LoadingState'
import { ActiveTasks } from '@/features/overview/ActiveTasks'
import { FindingClasses } from '@/features/overview/FindingClasses'
import { HealthStrip } from '@/features/overview/HealthStrip'
import { OpenFindings } from '@/features/overview/OpenFindings'
import { Targets } from '@/features/overview/Targets'
import { FindingsTrend, UsageTrend } from '@/features/overview/Trends'
import { useActiveTasks, useLogProcessOf, useOverviewHealth, useOverviewSnapshot } from '@/features/overview/useOverviewQueries'
import { RANGE_DAYS } from '@/features/overview/RangePills'
import { clockTime } from '@/lib/format'

// 概览页（/overview）：平台此刻的全貌——组件能不能干活、有多少问题待处理、集中在哪些类型、
// 哪个目标最需要先看、什么在跑、近期产出与消耗。每块回答一个别处回答不了的问题，不重复。只读，停在页面上时定时刷新；每块都链到承载完整数据的页面。
export function OverviewPage() {
  // 趋势时间窗进 URL（?days=7|14|30，缺省 14）：改档位是用户主动的筛选，新增一条历史。
  const [searchParams, setSearchParams] = useSearchParams()
  const rawDays = Number(searchParams.get('days'))
  const days = (RANGE_DAYS as readonly number[]).includes(rawDays) ? rawDays : 14
  const setDays = (d: number) =>
    setSearchParams((prev: URLSearchParams) => {
      const next = new URLSearchParams(prev)
      next.set('days', String(d))
      return next
    })
  const snapshot = useOverviewSnapshot(days)
  const health = useOverviewHealth()
  const active = useActiveTasks()
  const logOf = useLogProcessOf()
  const snap = snapshot.data
  const refreshing = snapshot.isFetching || health.isFetching || active.isFetching
  const refresh = () => {
    void snapshot.refetch()
    void health.refetch()
    void active.refetch()
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="概览">
        {snap && (
          <span className="text-xs tabular-nums text-text-tertiary" aria-live="polite">
            更新于 {clockTime(snap.generated_at)}
          </span>
        )}
        <Button variant="secondary" size="sm" className="w-9 px-0" aria-label="刷新" title="刷新" onClick={refresh}>
          <RotateCw className={refreshing ? 'motion-safe:animate-spin' : undefined} />
        </Button>
      </PageHeader>

      <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">
        <div className="mx-auto flex max-w-[1400px] flex-col gap-4">
          {health.data ? (
            <HealthStrip health={health.data} logOf={logOf} />
          ) : health.isError ? (
            <ErrorState title="组件状态读取失败" onRetry={() => void health.refetch()} className="py-6" />
          ) : null}

          {snapshot.isPending ? (
            <LoadingState className="py-20" />
          ) : snapshot.isError || !snap ? (
            <ErrorState
              message={snapshot.error instanceof Error ? snapshot.error.message : undefined}
              onRetry={() => void snapshot.refetch()}
              className="py-20"
            />
          ) : (
            // 第一排是要处理的问题（多少、什么类型），第二排是在哪里与正在做什么，第三排是近期走势。
            // 较窄时目标表与进行中的任务各占一行（目标表需要整行宽度才不被横向截断）。
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-12">
              <OpenFindings open={snap.findings.open} className="lg:col-span-5 2xl:col-span-4" />
              <FindingClasses classes={snap.findings.classes} className="lg:col-span-7 2xl:col-span-8" />
              <Targets targets={snap.targets} className="lg:col-span-12 2xl:col-span-7" />
              <ActiveTasks
                tasks={active.data?.items ?? []}
                total={snap.tasks.running + snap.tasks.paused}
                className="lg:col-span-12 2xl:col-span-5"
              />
              <FindingsTrend daily={snap.findings.daily} days={days} onDaysChange={setDays} className="lg:col-span-6" />
              <UsageTrend usage={snap.usage} days={days} onDaysChange={setDays} className="lg:col-span-6" />
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
