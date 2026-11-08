// <input type="datetime-local"> 的值格式（本地时区、分钟精度：YYYY-MM-DDTHH:mm）与
// API 使用的 UTC ISO 串互转。非法或空输入一律返回空串——空 = 不设该时间边界。

const pad = (n: number): string => String(n).padStart(2, '0')

/** datetime-local 控件值 → UTC ISO 串。 */
export function localInputToIso(local: string): string {
  if (!local) return ''
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

/** UTC ISO 串 → datetime-local 控件值（本地时区）。 */
export function isoToLocalInput(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/**
 * 控件新值 → ISO：控件值与原 ISO 的控件表示相同时原样保留原 ISO。
 * 时间范围控件一次回传起止两端，未改动的一端不应经「ISO→分钟精度→ISO」往返丢掉秒级精度。
 */
export function syncIsoWithLocalInput(prevIso: string, local: string): string {
  return isoToLocalInput(prevIso) === local ? prevIso : localInputToIso(local)
}
