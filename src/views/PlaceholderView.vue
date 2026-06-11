<script setup lang="ts">
// 占位页：阶段0 还没实现的路由统一用它，按路由 meta.title 显示「建设中」，避免空白/报错观感。
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { Tool } from '@vicons/tabler'
const route = useRoute()
const title = computed(() => (route.meta.title as string) || '页面')
</script>

<template>
  <div class="placeholder">
    <div class="ph-card">
      <span class="ph-icon"><Tool /></span>
      <h2>{{ title }}</h2>
      <p>该模块正在接入后端，敬请期待。</p>
    </div>
  </div>
</template>

<style scoped>
.placeholder { height: 100%; display: grid; place-items: center; padding: 40px; }
.ph-card {
  text-align: center;
  max-width: 380px;
  padding: 40px 36px;
  border: 1px dashed var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface);
}
.ph-icon {
  display: inline-grid;
  place-items: center;
  width: 56px;
  height: 56px;
  border-radius: 16px;
  background: var(--primary-soft);
  color: var(--primary);
  margin-bottom: 18px;
}
.ph-icon :deep(svg) { width: 28px; height: 28px; }
.ph-card h2 { margin: 0 0 8px; font-size: 19px; }
.ph-card p { margin: 0; color: var(--muted); font-size: 13.5px; line-height: 1.6; }
</style>
