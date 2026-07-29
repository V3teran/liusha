// 主题切换：默认暗黑色调（作战台风），可切浅色。
// 来源优先级 localStorage('liusha-theme') > 默认 dark（用户显式切过才记住偏好）。
// 写 document.documentElement.dataset.theme 驱动 CSS 变量；返回响应式给 Ant ConfigProvider 选 algorithm。
import { ref, watch } from 'vue'

export type ThemeName = 'dark' | 'light'
const STORAGE_KEY = 'liusha-theme'

function initial(): ThemeName {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'dark' || saved === 'light') return saved
  return 'dark' // 默认暗黑（不跟随系统；用户切换后才存偏好）
}

// 模块级单例：全局共享同一份主题状态。
const theme = ref<ThemeName>(initial())

function apply(t: ThemeName) {
  document.documentElement.dataset.theme = t
}
apply(theme.value)
watch(theme, apply)

export function useTheme() {
  function toggle() {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
    localStorage.setItem(STORAGE_KEY, theme.value)
  }
  return { theme, toggle }
}
