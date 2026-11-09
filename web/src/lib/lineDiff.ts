// 逐行文本差异（最长公共子序列）。用于「出厂已更新」时对比章程、技能正文等长文本：
// 规模是几百行，O(n·m) 足够，不引入第三方库。

export type DiffLine = { kind: 'same' | 'add' | 'del'; text: string }

/** 计算从 before 到 after 的逐行差异（删除行在前、新增行在后，与常见 diff 呈现一致）。 */
export function lineDiff(before: string, after: string): DiffLine[] {
  const a = before.split('\n')
  const b = after.split('\n')
  const n = a.length
  const m = b.length
  // lcs[i][j] = a[i:] 与 b[j:] 的最长公共子序列长度
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }
  const out: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ kind: 'same', text: a[i] })
      i++
      j++
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ kind: 'del', text: a[i++] })
    } else {
      out.push({ kind: 'add', text: b[j++] })
    }
  }
  while (i < n) out.push({ kind: 'del', text: a[i++] })
  while (j < m) out.push({ kind: 'add', text: b[j++] })
  return out
}

/** 只保留改动行及其前后 context 行，中间省略处以 null 分隔（长文本对比时聚焦改动）。 */
export function hunks(lines: DiffLine[], context = 2): Array<DiffLine | null> {
  const keep = new Array<boolean>(lines.length).fill(false)
  lines.forEach((l, idx) => {
    if (l.kind === 'same') return
    for (let k = Math.max(0, idx - context); k <= Math.min(lines.length - 1, idx + context); k++) keep[k] = true
  })
  const out: Array<DiffLine | null> = []
  let skipped = false
  lines.forEach((l, idx) => {
    if (keep[idx]) {
      if (skipped && out.length > 0) out.push(null)
      out.push(l)
      skipped = false
    } else {
      skipped = true
    }
  })
  return out
}
