// cron 五段式（分 时 日 月 周）的人话预览：「新建任务」填写与定时模板列表共用一份口径。

const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六']

/** 常见形态译成中文（每天 / 每周 / 每月 某时刻）；其余原样展示，段数不对判无效。 */
export function cronHuman(v: string): string {
  const p = v.trim().split(/\s+/)
  if (p.length !== 5) return '无效表达式'
  const [mi, h, dom, mon, dow] = p
  if (!/^\d+$/.test(mi) || !/^\d+$/.test(h)) return v
  const hm = `${h.padStart(2, '0')}:${mi.padStart(2, '0')}`
  if (dom === '*' && mon === '*' && dow === '*') return `每天 ${hm}`
  if (dom === '*' && mon === '*' && /^\d+$/.test(dow)) return `每周${WEEKDAYS[+dow % 7]} ${hm}`
  if (dow === '*' && mon === '*' && /^\d+$/.test(dom)) return `每月 ${dom} 日 ${hm}`
  return v
}
