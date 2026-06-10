<script setup lang="ts">
defineProps<{ tool: string; result: string; durationMs: number; err: string }>()
</script>
<template>
  <div class="card tool-result" data-card="tool-result" :data-error="!!err">
    <span class="meta"><code>{{ tool }}</code> · {{ durationMs }}ms</span>
    <pre v-if="err" class="err">{{ err }}</pre>
    <pre v-else class="out">{{ result }}</pre>
  </div>
</template>
