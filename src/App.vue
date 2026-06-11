<script setup lang="ts">
// 根：Naive 主题/消息/弹窗 provider 包裹 RouterView。
// 鉴权门暂时移除（之后再加认证）——直接进应用，免去每次输 X-API-Key。
import { computed } from 'vue'
import { NConfigProvider, NMessageProvider, NDialogProvider, darkTheme } from 'naive-ui'
import { useTheme } from './composables/useTheme'

const { theme } = useTheme()
const naiveTheme = computed(() => (theme.value === 'dark' ? darkTheme : null))

// 让 Naive 组件主色与本站 CSS token 对齐。
const overrides = {
  common: {
    primaryColor: '#7c8cff',
    primaryColorHover: '#93a0ff',
    primaryColorPressed: '#6a7af0',
    primaryColorSuppl: '#7c8cff',
    borderRadius: '9px',
    fontFamily:
      "Inter, system-ui, -apple-system, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', sans-serif",
  },
}
</script>

<template>
  <n-config-provider :theme="naiveTheme" :theme-overrides="overrides">
    <n-message-provider>
      <n-dialog-provider>
        <RouterView />
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>
