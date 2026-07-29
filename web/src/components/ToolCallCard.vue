<script setup lang="ts">
// 单个工具调用卡片：工具名标题 + 美化参数。
// 取代裸 JSON.stringify——审计/调试时一眼看清「调了哪个工具、传了什么参数」。
defineProps<{
  call: { id: string; name: string; args: string }
}>()
</script>

<template>
  <div class="tc-card">
    <div class="tc-head">
      <i class="tc-icon">⚙</i>
      <span class="tc-name mono">{{ call.name }}</span>
    </div>
    <pre v-if="call.args" class="tc-args mono">{{ call.args }}</pre>
    <span v-else class="tc-noargs">无参数</span>
  </div>
</template>

<style scoped>
.tc-card {
  border: 1px solid var(--border);
  border-radius: var(--radius, 8px);
  overflow: hidden;
  margin-bottom: 8px;
}
.tc-card:last-child { margin-bottom: 0; }
.tc-head {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 6px 10px;
  background: color-mix(in srgb, var(--primary) 9%, transparent);
  border-bottom: 1px solid var(--border);
}
.tc-icon { font-style: normal; font-size: 11px; opacity: 0.7; }
.tc-name { font-size: 12.5px; font-weight: 600; color: var(--primary); }
.tc-args {
  margin: 0;
  padding: 8px 11px;
  max-height: 240px;
  overflow: auto;
  background: var(--surface-2);
  font-size: 11.5px;
  line-height: 1.55;
  color: var(--text);
  white-space: pre-wrap;
  word-break: break-word;
}
.tc-noargs { display: block; padding: 6px 11px; font-size: 11.5px; color: var(--muted); opacity: 0.7; }
.mono { font-family: var(--mono); font-variant-numeric: tabular-nums; }
</style>
