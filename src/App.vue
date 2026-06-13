<script setup lang="ts">
// 根：Ant Design Vue 全局主题 provider 包裹 RouterView。
// 浅色为主、可切暗黑（useTheme → ConfigProvider algorithm）。主色 #1890ff 对齐 liusha-ui-old。
// 鉴权门暂移除（之后再加认证）——直接进应用。a-app 提供 message/modal 静态调用上下文。
import { computed } from 'vue'
import { theme as antTheme } from 'ant-design-vue'
import { useTheme } from './composables/useTheme'

const { theme } = useTheme()

const antConfig = computed(() => ({
  algorithm: theme.value === 'dark' ? antTheme.darkAlgorithm : antTheme.defaultAlgorithm,
  token: {
    colorPrimary: '#1890ff',
    borderRadius: 6,
    fontFamily:
      "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'PingFang SC', 'Microsoft YaHei', sans-serif",
  },
}))
</script>

<template>
  <a-config-provider :theme="antConfig">
    <a-app class="app-root">
      <RouterView />
    </a-app>
  </a-config-provider>
</template>

<style scoped>
.app-root {
  height: 100%;
}
</style>
