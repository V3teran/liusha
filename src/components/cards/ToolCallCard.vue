<script setup lang="ts">
import { ref } from 'vue'
defineProps<{ tool: string; args: string }>()
const open = ref(false)
</script>
<template>
  <div class="card tool-call" data-card="tool-call">
    <button class="tool-head" @click="open = !open">▸ 调用 <code>{{ tool }}</code></button>
    <pre v-if="open && args" class="args">{{ args }}</pre>
  </div>
</template>
