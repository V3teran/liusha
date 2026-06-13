<script setup lang="ts">
// 助手文字：LLM 叙述/总结/答复，按 markdown 富文本渲染（与推理卡同源消毒）。
import { computed } from 'vue'
import { renderMarkdown } from '../../lib/markdown'

const props = defineProps<{ content: string }>()
const html = computed(() => renderMarkdown(props.content))
</script>
<template>
  <div class="assistant markdown-body" data-card="assistant" v-html="html" />
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
}
</style>
