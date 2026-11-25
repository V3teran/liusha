import { useEffect, useState } from 'react'

// 服务端的值与本地估算差不到这个数就不重设基准：这点差是请求在路上的延迟，重设会让显示往回跳。
const RESYNC_MS = 1000

/**
 * 以服务端给的累计时长为基准的秒表：running 时逐秒往下走，返回此刻应显示的毫秒数。
 *
 * 不数「过了几拍」而按时间戳算：后台标签页的定时器会被浏览器节流，回到前台时下一拍直接给出
 * 正确值，不会慢。服务端每来回一次新的累计时长（serverMs 变化）就对一次表——差得多的才重设
 * 基准（续接后扣掉停顿、服务端与本地的钟有偏差），差不到一秒的当作延迟、不动基准。
 * 不在跑时原样显示服务端的值、不走。
 */
export function useStopwatch(serverMs: number | undefined, running: boolean): number | undefined {
  const [anchor, setAnchor] = useState(() => ({ ms: serverMs ?? 0, at: Date.now() }))
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!running) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [running])

  useEffect(() => {
    if (serverMs === undefined) return
    const estimate = anchor.ms + (Date.now() - anchor.at)
    if (Math.abs(serverMs - estimate) >= RESYNC_MS) setAnchor({ ms: serverMs, at: Date.now() })
    // 只在对表的时候读 anchor：它变了不需要重新对
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverMs, running])

  if (serverMs === undefined) return undefined
  // 走动时不少于服务端已经报过的值：两次轮询之间本地估的比服务端略超前是正常的，反过来不该出现
  return running ? Math.max(serverMs, anchor.ms + (now - anchor.at)) : serverMs
}
