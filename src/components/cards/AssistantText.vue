<script setup lang="ts">
// 助手文字：LLM 叙述/总结。保留换行与空白，便于阅读多行输出。
defineProps<{ content: string }>()
</script>
<template>
  <div class="assistant" data-card="assistant">{{ content }}</div>
</template>

<style scoped>
.assistant {
  align-self: flex-start;
  max-width: 85%;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 10px 13px;
  font-size: 14px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
