// 剪贴板：原生 navigator.clipboard 优先，失败回退 execCommand（http / 旧浏览器兜底）。
// 零依赖——项目本就无 clipboard 库，符合依赖克制风格。
export async function copyText(text: string): Promise<boolean> {
  if (!text) return false
  // 现代路径：需 https 或 localhost（secure context）。
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 落到 execCommand 兜底
  }
  // 回退：临时 textarea + execCommand（覆盖 http 内网 / 老浏览器）。
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.top = '-9999px'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}
