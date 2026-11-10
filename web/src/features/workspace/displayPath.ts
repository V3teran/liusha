
/** UUID 段形态（批次/任务目录名不进展示名，避免每行都拖一长串前缀）。 */
const uuidSeg = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

/** 展示名：剥掉前导 UUID 段后的相对路径（编辑器标题/通知用；复制原始路径用 title）。 */
export function displayPath(p: string): string {
  const segs = p.split('/')
  while (segs.length > 1 && uuidSeg.test(segs[0])) segs.shift()
  return segs.join('/')
}
